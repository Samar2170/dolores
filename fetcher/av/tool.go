package av

import (
	"context"
	"encoding/json"
	"fmt"

	"dolores/internal/tool"
)

// Tool names for the Alpha Vantage operations.
const (
	ToolTimeSeriesDaily = "av_time_series_daily"
	ToolGlobalQuote     = "av_global_quote"
)

// quoteArgs is the argument schema shared by both AV tools.
type quoteArgs struct {
	Symbol   string `json:"symbol"`
	Exchange string `json:"exchange,omitempty"`
}

// TimeSeriesDailyTool runs av.TimeSeriesDaily for one symbol via tool.Tool.
// Archiving to Archivus and MongoDB happens inside the client.
type TimeSeriesDailyTool struct {
	client *Client
}

// NewTimeSeriesDailyTool wraps client as a tool.
func NewTimeSeriesDailyTool(client *Client) *TimeSeriesDailyTool {
	return &TimeSeriesDailyTool{client: client}
}

func (t *TimeSeriesDailyTool) Name() string { return ToolTimeSeriesDaily }

func (t *TimeSeriesDailyTool) Description() string {
	return "Fetch Alpha Vantage TIME_SERIES_DAILY (end-of-day OHLCV, last ~100 sessions) for a symbol; archives the raw payload to storage and the market database."
}

func (t *TimeSeriesDailyTool) Execute(ctx context.Context, args json.RawMessage) error {
	a, err := decodeQuoteArgs(args)
	if err != nil {
		return fmt.Errorf("%s: %w", ToolTimeSeriesDaily, err)
	}
	if _, err := t.client.TimeSeriesDaily(ctx, a.Symbol, a.Exchange); err != nil {
		return fmt.Errorf("%s: %w", ToolTimeSeriesDaily, err)
	}
	return nil
}

// GlobalQuoteTool runs av.GlobalQuote for one symbol via tool.Tool.
type GlobalQuoteTool struct {
	client *Client
}

// NewGlobalQuoteTool wraps client as a tool.
func NewGlobalQuoteTool(client *Client) *GlobalQuoteTool {
	return &GlobalQuoteTool{client: client}
}

func (t *GlobalQuoteTool) Name() string { return ToolGlobalQuote }

func (t *GlobalQuoteTool) Description() string {
	return "Fetch Alpha Vantage GLOBAL_QUOTE (current price/volume) for a symbol; archives the raw payload to storage and the market database."
}

func (t *GlobalQuoteTool) Execute(ctx context.Context, args json.RawMessage) error {
	a, err := decodeQuoteArgs(args)
	if err != nil {
		return fmt.Errorf("%s: %w", ToolGlobalQuote, err)
	}
	if _, err := t.client.GlobalQuote(ctx, a.Symbol, a.Exchange); err != nil {
		return fmt.Errorf("%s: %w", ToolGlobalQuote, err)
	}
	return nil
}

func decodeQuoteArgs(args json.RawMessage) (quoteArgs, error) {
	var a quoteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return a, fmt.Errorf("decode args: %w", err)
	}
	if a.Symbol == "" {
		return a, fmt.Errorf("symbol is required")
	}
	if a.Exchange == "" {
		a.Exchange = "BSE"
	}
	return a, nil
}

// Compile-time interface checks.
var (
	_ tool.Tool = (*TimeSeriesDailyTool)(nil)
	_ tool.Tool = (*GlobalQuoteTool)(nil)
)
