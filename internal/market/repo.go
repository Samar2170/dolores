// Package market stores raw market-API payloads (Alpha Vantage, IndiaSM) as
// queryable MongoDB documents, one collection per output kind. Each document
// keeps the payload embedded so its fields can be queried with dot-notation.
package market

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
)

// Kind selects the collection a raw payload belongs to.
const (
	KindTimeSeriesDaily = "time_series_daily"
	KindGlobalQuote     = "global_quote"
	KindStock           = "stock"
)

// Tickertape kinds are the pageProps section keys; each maps to its own
// collection (see tickerCollections).
const (
	KindTickerCommentary          = "commentary"
	KindTickerFaq                 = "stockPageFaq"
	KindTickerIncomeAnnual        = "income-normal-annual"
	KindTickerIncomeInterim       = "income-normal-interim"
	KindTickerBalancesheetAnnual  = "balancesheet-normal-annual"
	KindTickerCashflowAnnual      = "cashflow-normal-annual"
	KindTickerPeers               = "peers-technical"
	KindTickerEventsCorpActions   = "events-corp-actions"
	KindTickerEventsAnnouncements = "events-announcements"
	KindTickerEventsLegal         = "events-legal"
)

// tickerCollections pairs each tickertape kind with its collection.
var tickerCollections = []struct {
	kind string
	coll string
}{
	{KindTickerCommentary, models.ColTickerCommentary},
	{KindTickerFaq, models.ColTickerFaq},
	{KindTickerIncomeAnnual, models.ColTickerIncomeAnnual},
	{KindTickerIncomeInterim, models.ColTickerIncomeInterim},
	{KindTickerBalancesheetAnnual, models.ColTickerBalancesheetAnnual},
	{KindTickerCashflowAnnual, models.ColTickerCashflowAnnual},
	{KindTickerPeers, models.ColTickerPeers},
	{KindTickerEventsCorpActions, models.ColTickerEventsCorpActions},
	{KindTickerEventsAnnouncements, models.ColTickerEventsAnnouncements},
	{KindTickerEventsLegal, models.ColTickerEventsLegal},
}

// Saver persists one raw fetcher payload. *Repo implements it; the fetcher
// packages depend on this interface so they stay decoupled from MongoDB.
type Saver interface {
	SaveRaw(ctx context.Context, kind, symbol, exchange, day string, raw []byte) error
}

// Repo archives raw fetcher payloads into MongoDB. When bound to a company
// (WithCompany), every document it writes is stamped with the company's ID.
type Repo struct {
	db        *mongo.Database
	companyID bson.ObjectID
}

// NewRepo creates a Repo writing to the given database.
func NewRepo(db *mongo.Database) *Repo {
	return &Repo{db: db}
}

// WithCompany returns a copy of the repo bound to one company. Payloads
// saved through the copy carry company_id, linking every market document
// back to the companies collection.
func (r *Repo) WithCompany(companyID bson.ObjectID) *Repo {
	if r == nil {
		return nil
	}
	bound := *r
	bound.companyID = companyID
	return &bound
}

// Migrate ensures the market collections and their unique indexes exist.
// Documents are upserted per (identity, day), so repeated fetches on the
// same day update in place instead of duplicating.
func Migrate(db *mongo.Database) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	specs := []struct {
		coll string
		keys bson.D
		name string
	}{
		{models.ColAVTimeSeries, bson.D{{Key: "symbol", Value: 1}, {Key: "exchange", Value: 1}, {Key: "day", Value: 1}}, "idx_avts_symbol_exchange_day"},
		{models.ColAVGlobalQuote, bson.D{{Key: "symbol", Value: 1}, {Key: "exchange", Value: 1}, {Key: "day", Value: 1}}, "idx_avgq_symbol_exchange_day"},
		{models.ColIndiasmStock, bson.D{{Key: "name", Value: 1}, {Key: "day", Value: 1}}, "idx_istk_name_day"},
	}
	for _, t := range tickerCollections {
		specs = append(specs, struct {
			coll string
			keys bson.D
			name string
		}{t.coll, bson.D{{Key: "symbol", Value: 1}, {Key: "day", Value: 1}}, "idx_tt_" + t.coll + "_symbol_day"})
	}
	for _, s := range specs {
		_, err := db.Collection(s.coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    s.keys,
			Options: options.Index().SetUnique(true).SetName(s.name),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveRaw upserts one raw payload keyed by (symbol/name, day). kind selects
// the collection; symbol is the Alpha Vantage symbol or the IndiaSM company
// name; exchange is only meaningful for Alpha Vantage payloads. A repo bound
// via WithCompany also stamps the payload with the company's ID.
func (r *Repo) SaveRaw(ctx context.Context, kind, symbol, exchange, day string, raw []byte) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("market: repo not initialised")
	}
	if symbol == "" {
		return fmt.Errorf("market: empty symbol")
	}

	payload, err := payloadDoc(raw)
	if err != nil {
		return fmt.Errorf("market: decode payload: %w", err)
	}

	var coll string
	var filter, set bson.M
	switch kind {
	case KindTimeSeriesDaily, KindGlobalQuote:
		coll = models.ColAVTimeSeries
		if kind == KindGlobalQuote {
			coll = models.ColAVGlobalQuote
		}
		filter = bson.M{"symbol": symbol, "exchange": exchange, "day": day}
	case KindStock:
		coll = models.ColIndiasmStock
		filter = bson.M{"name": symbol, "day": day}
	default:
		for _, t := range tickerCollections {
			if t.kind == kind {
				coll = t.coll
				filter = bson.M{"symbol": symbol, "day": day}
				break
			}
		}
		if coll == "" {
			return fmt.Errorf("market: unknown kind %q", kind)
		}
	}

	set = bson.M{"payload": payload, "fetched_at": now()}
	if !r.companyID.IsZero() {
		set["company_id"] = r.companyID
	}
	_, err = r.db.Collection(coll).UpdateOne(ctx, filter,
		bson.M{"$set": set, "$setOnInsert": bson.M{"created_at": now()}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// payloadDoc converts a raw JSON payload into an embedded BSON document (or
// array, for tickertape sections that are top-level lists) so its fields stay
// queryable from MongoDB.
func payloadDoc(raw []byte) (any, error) {
	var v any
	if err := bson.UnmarshalExtJSON(raw, false, &v); err != nil {
		return nil, err
	}
	switch v.(type) {
	case bson.D, bson.A:
		return v, nil
	default:
		return nil, fmt.Errorf("market: unexpected payload type %T", v)
	}
}

func now() time.Time { return time.Now().UTC() }
