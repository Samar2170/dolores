// Package tickertape extracts stock data from saved Tickertape stock pages
// (see __reference_files__/tickertape_kotak_resp.html): the page HTML embeds
// the full payload as JSON inside a <script id="__NEXT_DATA__"> tag, and the
// useful sections (commentary, FAQ, financial statements, peers, events) live
// under props.pageProps. ExtractToFile archives the extract to Archivus
// storage and saves each section into its own MongoDB collection (market.Repo).
package tickertape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"dolores/internal/market"
	"dolores/storage"
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

const (
	nextDataMarker = `id="__NEXT_DATA__"`

	fileTimeFormat = "2006-01-02T15-04-05"
	dayFormat      = "2006-01-02"
)

// Extract reads a saved Tickertape stock page for symbol (e.g. "KOTAKBANK")
// at htmlPath and returns the extracted stock data as indented JSON: the
// commentary, FAQ, financial statements, peers and event sections embedded in
// the page's __NEXT_DATA__ script.
func Extract(symbol, htmlPath string) ([]byte, error) {
	sections, err := extractSections(symbol, htmlPath)
	if err != nil {
		return nil, err
	}

	buf, err := json.MarshalIndent(sections, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("tickertape: %s: encode extract: %w", symbol, err)
	}
	return buf, nil
}

// extractSections reads a saved page and returns the extracted sections keyed
// by their pageProps key.
func extractSections(symbol, htmlPath string) (map[string]json.RawMessage, error) {
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
	return out, nil
}

// ExtractToFile runs Extract, writes the result to outPath, archives it to
// Archivus under <symbol>/ and saves each extracted section into its own
// MongoDB collection via saver (market.Repo). Both sinks are optional; a
// configured sink that fails aborts the save.
func ExtractToFile(ctx context.Context, symbol, htmlPath, outPath string, arch *storage.ArchivusClient, saver market.Saver) error {
	sections, err := extractSections(symbol, htmlPath)
	if err != nil {
		return err
	}

	buf, err := json.MarshalIndent(sections, "", "    ")
	if err != nil {
		return fmt.Errorf("tickertape: %s: encode extract: %w", symbol, err)
	}
	if err := os.WriteFile(outPath, buf, 0o644); err != nil {
		return fmt.Errorf("tickertape: write extract: %w", err)
	}

	if arch != nil {
		fileName := fmt.Sprintf("%s_tickertape_%s.json", symbol, time.Now().Format(fileTimeFormat))
		if err := arch.Upload(symbol+"/", []*storage.UploadFile{{Name: fileName, Content: buf}}); err != nil {
			return fmt.Errorf("tickertape: %s: archive extract: %w", symbol, err)
		}
	}

	if saver != nil {
		day := time.Now().Format(dayFormat)
		for _, key := range extractedKeys {
			section, ok := sections[key]
			if !ok {
				continue
			}
			if err := saver.SaveRaw(ctx, key, symbol, "", day, section); err != nil {
				return fmt.Errorf("tickertape: %s: save %s: %w", symbol, key, err)
			}
		}
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
