// Package tickertape extracts stock data from saved Tickertape stock pages
// (see __reference_files__/tickertape_kotak_resp.html): the page HTML embeds
// the full payload as JSON inside a <script id="__NEXT_DATA__"> tag, and the
// useful sections (commentary, FAQ, financial statements, peers, events) live
// under props.pageProps.
package tickertape

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// extractedKeys are the pageProps sections archived from a stock page.
var extractedKeys = []string{
	"commentary",
	"stockPageFaq",
	"income-normal-annual",
	"income-normal-interim",
	"balancesheet-normal-annual",
	"cashflow-normal-annual",
	"peers-technical",
	"events-corp-actions",
	"events-announcements",
	"events-legal",
}

const nextDataMarker = `id="__NEXT_DATA__"`

// Extract reads a saved Tickertape stock page for symbol (e.g. "KOTAKBANK")
// at htmlPath and returns the extracted stock data as indented JSON: the
// commentary, FAQ, financial statements, peers and event sections embedded in
// the page's __NEXT_DATA__ script.
func Extract(symbol, htmlPath string) ([]byte, error) {
	raw, err := os.ReadFile(htmlPath)
	if err != nil {
		return nil, fmt.Errorf("tickertape: read %s page: %w", symbol, err)
	}

	data, err := extractNextData(raw)
	if err != nil {
		return nil, fmt.Errorf("tickertape: %s: %w", symbol, err)
	}

	var page struct {
		Props struct {
			PageProps json.RawMessage `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("tickertape: %s: decode __NEXT_DATA__: %w", symbol, err)
	}
	if len(page.Props.PageProps) == 0 {
		return nil, fmt.Errorf("tickertape: %s: no pageProps in __NEXT_DATA__", symbol)
	}

	var pageProps map[string]json.RawMessage
	if err := json.Unmarshal(page.Props.PageProps, &pageProps); err != nil {
		return nil, fmt.Errorf("tickertape: %s: decode pageProps: %w", symbol, err)
	}

	out := make(map[string]json.RawMessage, len(extractedKeys))
	for _, key := range extractedKeys {
		if v, ok := pageProps[key]; ok {
			out[key] = v
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tickertape: %s: no stock data sections in pageProps", symbol)
	}

	buf, err := json.MarshalIndent(out, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("tickertape: %s: encode extract: %w", symbol, err)
	}
	return buf, nil
}

// ExtractToFile runs Extract and writes the result to outPath.
func ExtractToFile(symbol, htmlPath, outPath string) error {
	buf, err := Extract(symbol, htmlPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, buf, 0o644); err != nil {
		return fmt.Errorf("tickertape: write extract: %w", err)
	}
	return nil
}

// extractNextData returns the raw JSON between the __NEXT_DATA__ script tags.
func extractNextData(html []byte) ([]byte, error) {
	i := bytes.Index(html, []byte(nextDataMarker))
	if i < 0 {
		return nil, fmt.Errorf("no __NEXT_DATA__ script in HTML")
	}
	start := bytes.IndexByte(html[i:], '>')
	if start < 0 {
		return nil, fmt.Errorf("malformed __NEXT_DATA__ script tag")
	}
	start += i + 1

	end := bytes.Index(html[start:], []byte("</script>"))
	if end < 0 {
		return nil, fmt.Errorf("unterminated __NEXT_DATA__ script")
	}
	end += start

	data := bytes.TrimSpace(html[start:end])
	if len(data) == 0 {
		return nil, fmt.Errorf("empty __NEXT_DATA__ script")
	}
	return data, nil
}
