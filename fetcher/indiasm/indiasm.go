// Package indiasm provides a minimal client for the stock.indianapi.in API
// (see apitest/india_sm.ipynb): the /stock endpoint returns the company
// profile, financials, shareholding and recent news for an Indian stock.
// Raw JSON payloads are archived to Archivus storage.
package indiasm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"dolores/fetcher/config"
	"dolores/storage"
)

const (
	baseURL     = "https://stock.indianapi.in"
	httpTimeout = 60 * time.Second

	storageFolder  = "indiasm"
	fileTimeFormat = "2006-01-02T15-04-05"
)

// Client calls stock.indianapi.in and archives raw responses to Archivus.
type Client struct {
	apiKey  string
	storage *storage.ArchivusClient
	hc      *http.Client
}

// New creates a client using INDIAN_SM_API_KEY from fetcher/config (falling
// back to INDIAN_SM_API_KEY2, as in the notebook). Raw responses are uploaded
// to Archivus via storage.
func New(storage *storage.ArchivusClient) *Client {
	// apiKey := config.INDIAN_SM_API_KEY
	// if apiKey == "" {
	apiKey := config.INDIAN_SM_API_KEY2
	// }
	return NewWithKey(apiKey, storage)
}

// NewWithKey creates a client with an explicit API key.
func NewWithKey(apiKey string, storage *storage.ArchivusClient) *Client {
	return &Client{
		apiKey:  apiKey,
		storage: storage,
		hc:      &http.Client{Timeout: httpTimeout},
	}
}

// Stock fetches the /stock endpoint for name (e.g. "ITC") — company profile,
// financials, shareholding and recent news — and archives the raw payload to
// Archivus.
func (c *Client) Stock(ctx context.Context, name string) (json.RawMessage, error) {
	raw, err := c.fetch(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := c.save(name, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) fetch(ctx context.Context, name string) (json.RawMessage, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("indiasm: missing API key")
	}

	q := url.Values{}
	q.Set("name", name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/stock?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

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
		return nil, fmt.Errorf("indiasm: %s: %s", resp.Status, string(body))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("indiasm: empty response")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("indiasm: decode response: %w", err)
	}
	for _, key := range []string{"error", "message"} {
		if msg, ok := payload[key]; ok {
			return nil, fmt.Errorf("indiasm: %s", rawString(msg))
		}
	}
	if _, ok := payload["companyName"]; !ok {
		return nil, fmt.Errorf("indiasm: response missing %q", "companyName")
	}

	return json.RawMessage(body), nil
}

// save uploads the raw payload to Archivus under indiasm/stock/. If no storage
// is configured, the payload is not persisted.
func (c *Client) save(name string, raw []byte) error {
	if c.storage == nil {
		return nil
	}
	folder := storageFolder + "/stock"
	filename := fmt.Sprintf("%s_%s.json", name, time.Now().Format(fileTimeFormat))
	return c.storage.Upload(folder, []*storage.UploadFile{{Name: filename, Content: raw}})
}

func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw)
	}
	return s
}
