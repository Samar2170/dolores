// Package av provides a minimal Alpha Vantage client for the endpoints that
// work on a free API key (see apitest/alphavantage.ipynb): TIME_SERIES_DAILY
// and GLOBAL_QUOTE. Raw JSON payloads are archived to Archivus storage.
package av

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"dolores/fetcher/config"
	"dolores/storage"
)

const (
	baseURL        = "https://www.alphavantage.co/query"
	rateLimitDelay = 13 * time.Second // free tier: ~5 requests/min, 25/day
	httpTimeout    = 60 * time.Second

	// FunctionTimeSeriesDaily returns end-of-day OHLCV (last ~100 sessions).
	FunctionTimeSeriesDaily = "TIME_SERIES_DAILY"
	// FunctionGlobalQuote returns the current price/volume for a symbol.
	FunctionGlobalQuote = "GLOBAL_QUOTE"

	storageFolder  = "alphavantage"
	fileTimeFormat = "2006-01-02"
)

// responseKey is the top-level key a successful payload must contain.
var responseKey = map[string]string{
	FunctionTimeSeriesDaily: "Time Series (Daily)",
	FunctionGlobalQuote:     "Global Quote",
}

// Client calls Alpha Vantage and archives raw responses to Archivus.
type Client struct {
	apiKey  string
	storage *storage.ArchivusClient
	hc      *http.Client

	mu       sync.Mutex
	lastCall time.Time
}

// New creates a client using AV_API_KEY from fetcher/config. Raw responses
// are uploaded to Archivus via store.
func New(storage *storage.ArchivusClient) *Client {
	return NewWithKey(config.ALPHAVANTAGE_API_KEY, storage)
}

// NewWithKey creates a client with an explicit API key.
func NewWithKey(apiKey string, storage *storage.ArchivusClient) *Client {
	return &Client{
		apiKey:  apiKey,
		storage: storage,
		hc:      &http.Client{Timeout: httpTimeout},
	}
}

// TimeSeriesDaily fetches TIME_SERIES_DAILY for symbol (e.g. "ITC.BSE") and
// archives the raw payload to Archivus.
func (c *Client) TimeSeriesDaily(ctx context.Context, symbol string, exchange string) (json.RawMessage, error) {
	return c.fetchAndSave(ctx, FunctionTimeSeriesDaily, symbol, exchange)
}

// GlobalQuote fetches GLOBAL_QUOTE for symbol and archives the raw payload
// to Archivus.
func (c *Client) GlobalQuote(ctx context.Context, symbol, exchange string) (json.RawMessage, error) {
	return c.fetchAndSave(ctx, FunctionGlobalQuote, symbol, exchange)
}

func (c *Client) fetchAndSave(ctx context.Context, function, symbol, exchange string) (json.RawMessage, error) {
	raw, err := c.fetch(ctx, function, symbol, exchange)
	if err != nil {
		return nil, err
	}
	if err := c.save(function, symbol, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) fetch(ctx context.Context, function, symbol, exchange string) (json.RawMessage, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("av: missing API key")
	}
	if err := c.waitRateLimit(ctx); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("function", function)
	if exchange != "" {
		symbol = symbol + "." + exchange
	}
	q.Set("symbol", symbol)
	q.Set("apikey", c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("av: %s: %s", resp.Status, string(body))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("av: empty response")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("av: decode response: %w", err)
	}
	for _, key := range []string{"Error Message", "Note", "Information"} {
		if msg, ok := payload[key]; ok {
			return nil, fmt.Errorf("av: %s: %s", key, rawString(msg))
		}
	}
	if key, ok := responseKey[function]; ok {
		if _, ok := payload[key]; !ok {
			return nil, fmt.Errorf("av: %s: response missing %q", function, key)
		}
	}

	return json.RawMessage(body), nil
}

// save uploads the raw payload to Archivus under <symbol>/ as
// <symbol>_<function>_<date>.json.
// If no store is configured, the payload is not persisted.
func (c *Client) save(function, symbol string, raw []byte) error {
	if c.storage == nil {
		return nil
	}
	folder, fileName := c.resolvePath(symbol, function)
	return c.storage.Upload(folder, []*storage.UploadFile{{Name: fileName, Content: raw}})
}

func (c *Client) resolvePath(symbol, function string) (string, string) {
	if c.storage == nil {
		return "", ""
	}
	fileName := fmt.Sprintf("%s_%s_%s.json", symbol, function, time.Now().Format(fileTimeFormat))
	folder := symbol + "/"

	return folder, fileName
}

// waitRateLimit spaces requests at least rateLimitDelay apart to stay under
// the free-tier per-minute limit.
func (c *Client) waitRateLimit(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if wait := rateLimitDelay - time.Since(c.lastCall); wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	c.lastCall = time.Now()
	return nil
}

func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw)
	}
	return s
}
