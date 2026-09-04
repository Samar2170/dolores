// Package analysis runs the financial-analysis agent over everything stored
// for one company: the latest indiasm_stock payload, all key_metrics
// documents, the latest payload of every tickertape_* collection and the
// company's revenue_segmentation research resource when present.
package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/metrics"
	"dolores/internal/models"
	"dolores/internal/research"
)

// tickerCollections are the tickertape_* collections read for a symbol, in
// report order.
var tickerCollections = []string{
	models.ColTickerIncomeAnnual,
	models.ColTickerBalancesheetAnnual,
	models.ColTickerCashflowAnnual,
}

// indiaSMFinancialAnalysisKeys are the payload keys kept from the
// indiasm_stock document when it is collected for analysis.
var indiaSMFinancialAnalysisKeys = []string{
	"financials",
	"keyMetrics",
	"stockFinancialData",
}

// companyProfile is the company identity handed to the agent alongside the
// data, so every conclusion can be tied back to the company_id.
type companyProfile struct {
	CompanyID bson.ObjectID `json:"company_id"`
	Symbol    string        `json:"symbol"`
	Exchange  string        `json:"exchange,omitempty"`
	Name      string        `json:"name,omitempty"`
	Industry  string        `json:"industry,omitempty"`
	ISINCode  string        `json:"isin_code,omitempty"`
}

// datedPayload pairs a stored raw payload with the day it was fetched.
type datedPayload struct {
	Day     string          `json:"day"`
	Payload json.RawMessage `json:"payload"`
}

// Data is the full stored dataset for one company, handed to the
// financial-analysis agent as JSON. Sources are keyed by their collection or
// resource name; absent sources are simply missing from the map.
type Data struct {
	Symbol  string                     `json:"symbol"`
	Company companyProfile             `json:"company"`
	Sources map[string]json.RawMessage `json:"sources"`
}

// storedPayload is the common shape of a market payload document; indiasm
// documents use name instead of symbol.
type storedPayload struct {
	Symbol  string        `bson:"symbol"`
	Name    string        `bson:"name"`
	Day     string        `bson:"day"`
	Payload bson.RawValue `bson:"payload"`
}

// Collect gathers every stored source for the company: the latest
// indiasm_stock payload, all key_metrics documents, the latest payload of
// every tickertape_* collection and the revenue_segmentation research
// resource when one exists. A source that is absent or undecodable is
// skipped with a log line so the agent runs on whatever is available; the
// error is non-nil only on database failures or when nothing is stored.
func Collect(ctx context.Context, db *mongo.Database, co *models.Company) (*Data, error) {
	d := &Data{
		Symbol: co.Symbol,
		Company: companyProfile{
			CompanyID: co.ID,
			Symbol:    co.Symbol,
			Exchange:  co.Exchange,
			Name:      co.Name,
			Industry:  co.Industry,
			ISINCode:  co.ISINCode,
		},
		Sources: make(map[string]json.RawMessage),
	}

	if raw := latestPayload(ctx, db, models.ColIndiasmStock, bson.M{"name": co.Symbol}, co.Symbol); raw != nil {
		d.Sources[models.ColIndiasmStock] = filterIndiasmPayload(raw)
	}

	if raw, err := keyMetrics(ctx, db, co.Symbol); err != nil {
		return nil, err
	} else if raw != nil {
		d.Sources[models.ColKeyMetrics] = raw
	}

	for _, coll := range tickerCollections {
		raw := latestPayload(ctx, db, coll, bson.M{"symbol": co.Symbol}, co.Symbol)
		if raw != nil {
			d.Sources[coll] = raw
		}
	}

	if raw := revenueSegmentation(db, co); raw != nil {
		d.Sources[models.SegmentRevenueSplit] = raw
	}

	if len(d.Sources) == 0 {
		return nil, fmt.Errorf("analysis: no stored data for %s (run api_data, tickertape and key_metrics first)", co.Symbol)
	}
	return d, nil
}

// latestPayload fetches the newest payload from one collection for the given
// filter and returns it as dated JSON; nil when nothing is stored or the
// payload cannot be decoded (logged and skipped).
func latestPayload(ctx context.Context, db *mongo.Database, coll string, filter bson.M, symbol string) json.RawMessage {
	var d storedPayload
	err := db.Collection(coll).FindOne(ctx, filter,
		options.FindOne().SetSort(bson.M{"day": -1})).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		log.Printf("[analysis] %s: no %s data stored", symbol, coll)
		return nil
	case err != nil:
		log.Printf("[analysis] %s: query %s failed: %v - skipping source", symbol, coll, err)
		return nil
	}
	payload, err := payloadJSON(d.Payload)
	if err != nil {
		log.Printf("[analysis] %s: decode %s payload: %v - skipping source", symbol, coll, err)
		return nil
	}
	buf, err := json.Marshal(datedPayload{Day: d.Day, Payload: payload})
	if err != nil {
		log.Printf("[analysis] %s: encode %s: %v - skipping source", symbol, coll, err)
		return nil
	}
	log.Printf("[analysis] %s: %s (day %s, %d bytes)", symbol, coll, d.Day, len(buf))
	return buf
}

// filterIndiasmPayload keeps only the indiaSMFinancialAnalysisKeys entries one
// level under the payload object and re-encodes the dated payload; raw is
// returned unchanged when it cannot be parsed or the payload is not an object.
func filterIndiasmPayload(raw json.RawMessage) json.RawMessage {
	var dated struct {
		Day     string                     `json:"day"`
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &dated); err != nil {
		return raw
	}
	if dated.Payload == nil {
		return raw
	}
	filtered := make(map[string]json.RawMessage, len(indiaSMFinancialAnalysisKeys))
	for _, k := range indiaSMFinancialAnalysisKeys {
		if v, ok := dated.Payload[k]; ok {
			filtered[k] = v
		}
	}
	payload, err := json.Marshal(filtered)
	if err != nil {
		return raw
	}
	buf, err := json.Marshal(datedPayload{Day: dated.Day, Payload: payload})
	if err != nil {
		return raw
	}
	return buf
}

// keyMetrics loads every key_metrics document for symbol (one per reporting
// basis) and returns them as a JSON array; nil when none are stored.
func keyMetrics(ctx context.Context, db *mongo.Database, symbol string) (json.RawMessage, error) {
	cur, err := db.Collection(models.ColKeyMetrics).Find(ctx, bson.M{"symbol": symbol},
		options.Find().SetSort(bson.M{"reporting": 1}))
	if err != nil {
		return nil, fmt.Errorf("analysis: query %s: %w", models.ColKeyMetrics, err)
	}
	defer cur.Close(ctx)

	var docs []metrics.KeyMetrics
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("analysis: decode %s: %w", models.ColKeyMetrics, err)
	}
	if len(docs) == 0 {
		log.Printf("[analysis] %s: no key_metrics stored", symbol)
		return nil, nil
	}
	buf, err := json.Marshal(docs)
	if err != nil {
		return nil, fmt.Errorf("analysis: encode %s: %w", models.ColKeyMetrics, err)
	}
	log.Printf("[analysis] %s: key_metrics (%d docs, %d bytes)", symbol, len(docs), len(buf))
	return buf, nil
}

// revenueSegmentation returns the company's revenue_segmentation research
// resource as JSON, or nil when it does not exist (the analysis continues
// without it).
func revenueSegmentation(db *mongo.Database, co *models.Company) json.RawMessage {
	res, err := research.GetResource(db, co.ID, models.SegmentRevenueSplit)
	if err != nil {
		log.Printf("[analysis] %s: revenue_segmentation lookup failed: %v", co.Symbol, err)
		return nil
	}
	if res == nil {
		log.Printf("[analysis] %s: no revenue_segmentation resource - continuing without it", co.Symbol)
		return nil
	}
	buf, err := json.Marshal(res)
	if err != nil {
		log.Printf("[analysis] %s: encode revenue_segmentation: %v", co.Symbol, err)
		return nil
	}
	log.Printf("[analysis] %s: revenue_segmentation resource (%d bytes)", co.Symbol, len(buf))
	return buf
}

// payloadJSON decodes a stored payload value (document or array) into JSON.
func payloadJSON(v bson.RawValue) (json.RawMessage, error) {
	switch v.Type {
	case bson.TypeEmbeddedDocument:
		var m bson.M
		if err := v.Unmarshal(&m); err != nil {
			return nil, err
		}
		return json.Marshal(m)
	case bson.TypeArray:
		var a bson.A
		if err := v.Unmarshal(&a); err != nil {
			return nil, err
		}
		return json.Marshal(a)
	default:
		return nil, fmt.Errorf("unexpected payload type %s", v.Type)
	}
}
