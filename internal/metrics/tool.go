package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"dolores/internal/tool"
)

// ToolCompute is the tool name for key-metrics computation.
const ToolCompute = "metrics_compute"

// ComputeArgs is the argument schema for the metrics_compute tool. An empty
// symbol computes metrics for every symbol with tickertape financials.
type ComputeArgs struct {
	Symbol string `json:"symbol,omitempty"`
}

// ComputeTool computes key metrics from stored tickertape financials and
// upserts them into the key_metrics collection, via tool.Tool.
type ComputeTool struct {
	db *mongo.Database
}

// NewComputeTool creates the tool against the given database.
func NewComputeTool(db *mongo.Database) *ComputeTool {
	return &ComputeTool{db: db}
}

func (t *ComputeTool) Name() string { return ToolCompute }

func (t *ComputeTool) Description() string {
	return "Compute key metrics (growth, margins, returns, health, cashflow, valuation) from stored tickertape financials and upsert them into key_metrics. Args: {\"symbol\": \"ITC\"} for one symbol, {} for all symbols."
}

func (t *ComputeTool) Execute(ctx context.Context, args json.RawMessage) error {
	var a ComputeArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return fmt.Errorf("%s: decode args: %w", ToolCompute, err)
		}
	}
	if a.Symbol != "" {
		doc, err := ComputeSymbol(ctx, t.db, a.Symbol)
		if err != nil {
			return fmt.Errorf("%s: %w", ToolCompute, err)
		}
		if doc == nil {
			log.Printf("[key_metrics] %s: no tickertape financial statements stored", a.Symbol)
			return nil
		}
		log.Printf("[key_metrics] %s stored: %s-%s (%d years, %s)",
			doc.Symbol, doc.FirstFY, doc.LastFY, doc.YearsCount, doc.Reporting)
		return nil
	}

	n, err := ComputeAll(ctx, t.db)
	if err != nil {
		return fmt.Errorf("%s: %w", ToolCompute, err)
	}
	log.Printf("[key_metrics] %d symbols stored", n)
	return nil
}

// Compile-time interface check.
var _ tool.Tool = (*ComputeTool)(nil)
