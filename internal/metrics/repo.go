package metrics

// Repo-side plumbing: load the latest tickertape statements for a symbol from
// MongoDB, compute the key metrics and upsert them into the key_metrics
// collection.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
)

// Migrate ensures the key_metrics collection and its unique (symbol,
// reporting) index exist.
func Migrate(db *mongo.Database) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := db.Collection(models.ColKeyMetrics).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "reporting", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_key_metrics_symbol_reporting"),
	})
	return err
}

// latestDoc is the stored shape of a tickertape payload document.
type latestDoc struct {
	Symbol  string        `bson:"symbol"`
	Day     string        `bson:"day"`
	Payload bson.RawValue `bson:"payload"`
}

// latestRows fetches the newest payload for a symbol from one tickertape
// collection and decodes it as rows; a missing document returns (nil, "", nil).
func latestRows[T any](ctx context.Context, coll *mongo.Collection, symbol string) ([]T, string, error) {
	var d latestDoc
	err := coll.FindOne(ctx, bson.M{"symbol": symbol},
		options.FindOne().SetSort(bson.M{"day": -1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var rows []T
	if err := d.Payload.Unmarshal(&rows); err != nil {
		return nil, "", fmt.Errorf("decode %s payload: %w", coll.Name(), err)
	}
	return rows, d.Day, nil
}

// LoadStatements loads and aligns the latest tickertape statements for a
// symbol; nil when no income statement data exists.
func LoadStatements(ctx context.Context, db *mongo.Database, symbol string) (*Statements, string, error) {
	income, incDay, err := latestRows[IncomeRow](ctx, db.Collection(models.ColTickerIncomeAnnual), symbol)
	if err != nil {
		return nil, "", err
	}
	if len(income) == 0 {
		return nil, "", nil
	}
	balance, balDay, err := latestRows[BalanceRow](ctx, db.Collection(models.ColTickerBalancesheetAnnual), symbol)
	if err != nil {
		return nil, "", err
	}
	cashflow, cfDay, err := latestRows[CashflowRow](ctx, db.Collection(models.ColTickerCashflowAnnual), symbol)
	if err != nil {
		return nil, "", err
	}

	st := BuildStatements(symbol, income, balance, cashflow)
	sourceDay := maxDay(incDay, balDay, cfDay)
	return st, sourceDay, nil
}

func maxDay(days ...string) string {
	max := ""
	for _, d := range days {
		if d > max {
			max = d
		}
	}
	return max
}

// LatestStockSnapshot returns the current price and market cap for a symbol
// from the latest indiasm_stock payload (stockDetailsReusableData, falling
// back to currentPrice for the quote). nil when nothing is stored.
func LatestStockSnapshot(ctx context.Context, db *mongo.Database, symbol string) (*StockSnapshot, error) {
	var d latestDoc
	err := db.Collection(models.ColIndiasmStock).FindOne(ctx, bson.M{"name": symbol},
		options.FindOne().SetSort(bson.M{"day": -1})).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var p struct {
		CurrentPrice struct {
			BSE string `bson:"BSE"`
			NSE string `bson:"NSE"`
		} `bson:"currentPrice"`
		Details struct {
			Price     string `bson:"price"`
			Close     string `bson:"close"`
			MarketCap string `bson:"marketCap"`
		} `bson:"stockDetailsReusableData"`
	}
	if err := d.Payload.Unmarshal(&p); err != nil {
		return nil, fmt.Errorf("decode indiasm_stock payload: %w", err)
	}

	priceStr := firstNonEmpty(p.Details.Price, p.CurrentPrice.NSE, p.CurrentPrice.BSE, p.Details.Close)
	price, err := strconv.ParseFloat(strings.TrimSpace(priceStr), 64)
	if err != nil || price <= 0 {
		return nil, nil
	}

	snap := &StockSnapshot{Price: price, Day: d.Day, Source: "indiasm_stock"}
	if mc, err := strconv.ParseFloat(strings.TrimSpace(p.Details.MarketCap), 64); err == nil && mc > 0 {
		snap.MarketCap = mc
	}
	return snap, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ComputeSymbol loads the statements for one symbol, computes its metrics and
// upserts them into key_metrics. Returns (nil, nil) when the symbol has no
// financial statements stored.
func ComputeSymbol(ctx context.Context, db *mongo.Database, symbol string) (*KeyMetrics, error) {
	st, sourceDay, err := LoadStatements(ctx, db, symbol)
	if err != nil {
		return nil, err
	}
	if st == nil || len(st.Years) == 0 {
		return nil, nil
	}

	doc := Compute(st)
	doc.SourceDay = sourceDay

	snap, err := LatestStockSnapshot(ctx, db, symbol)
	if err != nil {
		log.Printf("[key_metrics] %s: indiasm price lookup failed: %v", symbol, err)
	}
	doc.Valuation = ComputeValuation(st, doc.Years, snap, &doc.SourceNotes)

	coll := db.Collection(models.ColKeyMetrics)
	_, err = coll.UpdateOne(ctx,
		bson.M{"symbol": doc.Symbol, "reporting": doc.Reporting},
		bson.M{
			"$set":         doc,
			"$setOnInsert": bson.M{"created_at": doc.ComputedAt},
		},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// ComputeAll computes metrics for every symbol with tickertape financials.
// Symbols that fail are logged and skipped. Returns the number of symbols
// successfully stored.
func ComputeAll(ctx context.Context, db *mongo.Database) (int, error) {
	var symbols []string
	if err := db.Collection(models.ColTickerIncomeAnnual).
		Distinct(ctx, "symbol", bson.M{}).Decode(&symbols); err != nil {
		return 0, err
	}

	stored := 0
	for _, sym := range symbols {
		select {
		case <-ctx.Done():
			return stored, ctx.Err()
		default:
		}
		doc, err := ComputeSymbol(ctx, db, sym)
		switch {
		case err != nil:
			log.Printf("[key_metrics] %s FAILED: %v", sym, err)
		case doc == nil:
			log.Printf("[key_metrics] %s skipped: no financial statements", sym)
		default:
			log.Printf("[key_metrics] %s stored: %s-%s (%d years, %s)",
				sym, doc.FirstFY, doc.LastFY, doc.YearsCount, doc.Reporting)
			stored++
		}
	}
	return stored, nil
}
