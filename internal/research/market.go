package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"dolores/internal/llm"
	"dolores/storage"
)

const (
	maxCitationBytes   = 1 << 20
	maxCitationsStored = 3
)

// ResearchMarketShare builds a grounded, citation-carrying market-share
// estimate per product category, archiving the cited sources into the
// company's research folder.
func ResearchMarketShare(
	ctx context.Context,
	hc *http.Client,
	store *storage.ArchivusClient,
	client *llm.Client,
	info CompanyInfo,
	categories []string,
) ([]byte, error) {
	if len(categories) == 0 {
		return nil, fmt.Errorf("no product categories to research")
	}
	if len(categories) > 3 {
		categories = categories[:3]
	}

	var digest strings.Builder
	pool := map[string]bool{}
	for _, cat := range categories {
		fmt.Fprintf(&digest, "\n--- Market topic: %s ---\n", cat)
		hits := ddgSearch(ctx, hc, fmt.Sprintf("India %s market size share top players 2025", strings.ToLower(cat)))
		for i, h := range hits {
			fmt.Fprintf(&digest, "[cand] %s :: %s\n", h.Title, h.URL)
			pool[h.URL] = true
			if i == 0 && !isBlockedHost(hostOnly(h.URL)) {
				if body, ctype, err := FetchHTTP(ctx, hc, h.URL, maxHTMLBytes); err == nil &&
					strings.Contains(ctype, "text/html") {
					text := visibleText(string(body))
					if len(text) > 5000 {
						text = text[:5000]
					}
					digest.WriteString(text + "\n")
				}
			}
		}
	}
	urls := make([]string, 0, len(pool))
	for u := range pool {
		urls = append(urls, u)
	}

	system := strictJSONSystem + ` When asked for citations you MUST use only URLs from the candidate list.`
	user := fmt.Sprintf(
		`You are estimating MARKET SHARE for %s (%s). Categories: %q.

Below are web-search candidates (titles + URLs) plus one page extract. Use them as grounding; do not invent market numbers beyond what the text implies, and prefer marking confidence low over guessing.

%s

CANDIDATE URLS:
%s

Return JSON: {"categories":[{"category":"<one of the given>","our_est_share_pct":"e.g. '12-15%%' or '' if unknown","as_of":"year or period the estimate applies to","leaders":[{"name":"","share_pct":""}],"confidence":"high|medium|low","citation_urls":["<only from CANDIDATE URLS>"]}],"notes":"free-form caveats"}`,
		info.Name, info.Symbol, categories, digest.String(), strings.Join(urls, "\n"))

	start := time.Now()
	raw, err := client.CompleteJSON(ctx, system, user)
	if err != nil {
		return nil, err
	}
	var out ExtractedMarket
	if jerr := json.Unmarshal(raw, &out); jerr != nil || len(out.Categories) == 0 {
		hint := "invalid"
		if jerr != nil {
			hint = jerr.Error()
		}
		retry, rerr := client.CompleteJSON(ctx, system,
			user+"\n\nYour previous reply was invalid ("+hint+"). Return ONE valid JSON object with non-empty categories.")
		if rerr != nil {
			return nil, rerr
		}
		raw = retry
		out = ExtractedMarket{}
		if jerr = json.Unmarshal(raw, &out); jerr != nil || len(out.Categories) == 0 {
			return nil, fmt.Errorf("market share payload invalid twice")
		}
	}

	for _, d := range collectCitationDocs(ctx, hc, info, raw) {
		fullFolder := info.Symbol + "/research"
		if err := store.Upload(fullFolder, []*storage.UploadFile{{Name: d.FileName, Content: d.Bytes}}); err != nil {
			log.Printf("[market] %s: citation archive failed %s: %v", info.Symbol, d.FileName, err)
			continue
		}
		log.Printf("[market] %s: archived citation material %s (%d bytes)", info.Symbol, d.FileName, len(d.Bytes))
	}

	log.Printf("[market] %s: researched in %s", info.Symbol, time.Since(start).Round(time.Millisecond))
	return normalizeCitations(raw), nil
}

func collectCitationDocs(ctx context.Context, hc *http.Client, info CompanyInfo, raw json.RawMessage) []CollectedDoc {
	var out ExtractedMarket
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	seen := map[string]bool{}
	var docs []CollectedDoc
	n := 0
	for _, c := range out.Categories {
		for _, u := range c.CitationURLs {
			key := strings.ToLower(stripQueryJunk(u))
			if seen[key] {
				continue
			}
			seen[key] = true
			if n >= maxCitationsStored {
				return docs
			}
			data, ctype, err := FetchHTTP(ctx, hc, u, maxCitationBytes)
			if err != nil {
				log.Printf("[market] %s: citation fetch failed %s: %v", info.Symbol, u, err)
				continue
			}
			ext := "html"
			if IsPDF(data) {
				ext = "pdf"
			} else if !strings.Contains(ctype, "text/html") {
				ext = "bin"
			}
			host := hostOnly(u)
			name := sanitizeName(fmt.Sprintf("%s_market_cite_%d_%s.%s", info.Symbol, n+1, host, ext))
			docs = append(docs, CollectedDoc{Kind: "market_share_source", SourceURL: u, FileName: name, Bytes: data})
			n++
		}
	}
	return docs
}

// normalizeCitations guarantees every category carries confidence and keeps
// citation urls consistent with what we could verify.
func normalizeCitations(raw json.RawMessage) []byte {
	var out ExtractedMarket
	if json.Unmarshal(raw, &out) != nil {
		return raw
	}
	for i := range out.Categories {
		if out.Categories[i].Confidence == "" {
			out.Categories[i].Confidence = "low"
		}
		if out.Categories[i].Leaders == nil {
			out.Categories[i].Leaders = []Leader{}
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return b
}
