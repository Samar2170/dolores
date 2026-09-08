package indiasm

import (
	"context"
	"encoding/json"
	"fmt"

	"dolores/internal/tool"
)

// ToolStock is the tool name for the IndiaSM stock fetch.
const ToolStock = "indiasm_stock"

// stockArgs is the argument schema for the indiasm_stock tool.
type stockArgs struct {
	Symbol string `json:"symbol"`
}

// StockTool runs indiasm.Client.Stock for one symbol via tool.Tool.
// Archiving to Archivus and MongoDB happens inside the client.
type StockTool struct {
	client *Client
}

// NewStockTool wraps client as a tool.
func NewStockTool(client *Client) *StockTool {
	return &StockTool{client: client}
}

func (t *StockTool) Name() string { return ToolStock }

func (t *StockTool) Description() string {
	return "Fetch the stock.indianapi.in /stock payload (company profile, financials, shareholding, recent news) for an Indian stock symbol; archives the raw payload to storage and the market database."
}

func (t *StockTool) Execute(ctx context.Context, args json.RawMessage) error {
	var a stockArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("%s: decode args: %w", ToolStock, err)
	}
	if a.Symbol == "" {
		return fmt.Errorf("%s: symbol is required", ToolStock)
	}
	if _, err := t.client.Stock(ctx, a.Symbol); err != nil {
		return fmt.Errorf("%s: %w", ToolStock, err)
	}
	return nil
}

// Compile-time interface check.
var _ tool.Tool = (*StockTool)(nil)
