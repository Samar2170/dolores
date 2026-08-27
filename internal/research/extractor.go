package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"dolores/internal/llm"
	"dolores/internal/models"
)

// ErrNoText signals that no document provided a usable text layer.
var ErrNoText = errors.New("no usable text layer in archived documents")

type PageRef struct{}

type ExtractedProducts struct {
	BusinessSummary   string            `json:"business_summary"`
	ProductCategories []ProductCategory `json:"product_categories"`
	Confidence        string            `json:"confidence"`
	PageReferences    []string          `json:"page_references"`
}

type ProductCategory struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	RevenueShareHint string `json:"revenue_share_hint,omitempty"`
}

type ExtractedInputs struct {
	Manufacturing  bool       `json:"manufacturing"`
	Inputs         []RawInput `json:"inputs"`
	Confidence     string     `json:"confidence"`
	PageReferences []string   `json:"page_references"`
}

type RawInput struct {
	Material        string `json:"material"`
	UsedFor         string `json:"used_for"`
	SourcingRegions string `json:"sourcing_regions,omitempty"`
}

type ExtractedRevenue struct {
	FY             string           `json:"fy"`
	Currency       string           `json:"currency"`
	Segments       []RevenueSegment `json:"segments"`
	Confidence     string           `json:"confidence"`
	PageReferences []string         `json:"page_references"`
}

type RevenueSegment struct {
	Name              string `json:"name"`
	RevenuePctOrValue string `json:"revenue_pct_or_value"`
	Note              string `json:"note,omitempty"`
}

type ExtractedMarket struct {
	Categories []MarketCategory `json:"categories"`
	Notes      string           `json:"notes,omitempty"`
}

type MarketCategory struct {
	Category       string   `json:"category"`
	OurEstSharePct string   `json:"our_est_share_pct"`
	AsOf           string   `json:"as_of,omitempty"`
	Leaders        []Leader `json:"leaders"`
	Confidence     string   `json:"confidence"`
	CitationURLs   []string `json:"citation_urls"`
}

type Leader struct {
	Name     string `json:"name"`
	SharePct string `json:"share_pct,omitempty"`
}

var topicKeywords = map[string][]string{
	models.SegmentProducts:     {"product", "portfolio", "offering", "segment", "category", "brand"},
	models.SegmentRawInputs:    {"raw material", "raw materials", "key input", "sourced", "procure", "procurement", "supply", "commodity"},
	models.SegmentRevenueSplit: {"segment", "revenue from operations", "segment reporting", "business wise", "geograph"},
}

const (
	perSourceCharBudget = 18000
	totalContextBudget  = 46000
)

// BuildContext picks the most relevant page texts out of every document and
// returns them labelled with their source title.
func BuildContext(docs []CollectedDoc, keywords []string) string {
	var sb strings.Builder
	budgetLeft := totalContextBudget
	for _, d := range docs {
		if d.Kind == docKindIRIndex || len(d.Bytes) == 0 {
			continue
		}
		if strings.HasSuffix(d.FileName, ".txt") {
			text := string(d.Bytes)
			if len(text) > perSourceCharBudget {
				text = text[:perSourceCharBudget]
			}
			fmt.Fprintf(&sb, "\n===== SOURCE: %s =====\n%s\n", d.Title, text)
			budgetLeft -= len(text)
			continue
		}
		pages, total, _, err := ExtractPDFPages(d.Bytes)
		if err != nil {
			log.Printf("[context] %s: pdf parse failed: %v", d.FileName, err)
			continue
		}
		if len(pages) == 0 {
			log.Printf("[context] %s: %d/%d pages yielded text (likely scanned)", d.FileName, len(pages), total)
			continue
		}
		chunk := SelectPages(pages, keywords, minInt(perSourceCharBudget, maxInt(0, budgetLeft)))
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		fmt.Fprintf(&sb, "\n===== SOURCE: %s (FY %s) =====\n%s\n", d.Title, d.FY, chunk)
		budgetLeft -= len(chunk)
	}
	return sb.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const strictJSONSystem = `You are an equity-research data extractor. You ALWAYS reply with exactly one valid JSON object, no markdown fences, no commentary. Only state facts explicitly supported by the SOURCE text. When information is missing, leave the field empty or the array empty instead of guessing, and lower the confidence field.`

// ExtractTopics produces the structured JSON payload for one analysis segment.
func ExtractTopics(ctx context.Context, client *llm.Client, docs []CollectedDoc, segment string) ([]byte, error) {
	kws, ok := topicKeywords[segment]
	if !ok {
		return nil, fmt.Errorf("unknown segment %q", segment)
	}
	body := BuildContext(docs, kws)
	if strings.TrimSpace(body) == "" {
		return nil, ErrNoText
	}

	var instructions string
	switch segment {
	case models.SegmentProducts:
		instructions = `Task: identify WHAT THIS COMPANY MAKES/SELLS.
Return JSON: {"business_summary": "...", "product_categories":[{"name":"...","description":"...","revenue_share_hint":"e.g. 43% of revenue"}], "confidence":"high|medium|low", "page_references":["[page 12] etc"]}.
Include services alongside physical products; page_references should cite the [page N] markers present in the source text.`
	case models.SegmentRawInputs:
		instructions = `Task: identify INPUT RAW MATERIALS this company consumes (manufacturing). For service companies set manufacturing=false and inputs=[].
Return JSON: {"manufacturing": true|false, "inputs":[{"material":"e.g. hot rolled steel coils","used_for":"...","sourcing_regions":"if stated"}], "confidence":"...", "page_references":[...]}.`
	default:
		instructions = `Task: capture REVENUE BIFURCATION by product/business/segment for the most recent FY mentioned.
Return JSON: {"fy":"e.g. FY2025-26","currency":"INR crore unless stated otherwise","segments":[{"name":"...","revenue_pct_or_value":"verbatim number with unit/%","note":"optional"}], "confidence":"...", "page_references":[...]}.`
	}

	user := fmt.Sprintf("%s\n\nHere is extracted document text:\n%s\n\n%s", strictIntro(), body, instructions)
	return runStrictJSON(ctx, client, user, segment)
}

func strictIntro() string {
	return "SOURCE TEXT BEGINS ===>"
}

// runStrictJSON calls the model, validates into the right struct for the
// segment, and retries once with a corrective hint on structural problems.
func runStrictJSON(ctx context.Context, client *llm.Client, user, segment string) ([]byte, error) {
	raw, err := client.CompleteJSON(ctx, strictJSONSystem, user)
	if err == nil {
		if perr := validateSegment(raw, segment); perr == nil {
			return raw, nil
		} else {
			err = perr
		}
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	retryMsg := fmt.Sprintf("%s\n\nIMPORTANT: your previous reply could not be parsed/validated for this schema (%v). Reply again with ONE valid JSON object matching the exact keys requested.", user, err)
	raw2, err2 := client.CompleteJSON(ctx, strictJSONSystem, retryMsg)
	if err2 != nil {
		return nil, fmt.Errorf("llm extraction failed twice: last=%w", err2)
	}
	if verr := validateSegment(raw2, segment); verr != nil {
		return nil, fmt.Errorf("llm extraction still invalid: %w", verr)
	}
	return raw2, nil
}

func validateSegment(raw json.RawMessage, segment string) error {
	switch segment {
	case models.SegmentProducts:
		var v ExtractedProducts
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if v.BusinessSummary == "" && len(v.ProductCategories) == 0 {
			return errors.New("empty products payload")
		}
	case models.SegmentRawInputs:
		var v ExtractedInputs
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
	case models.SegmentRevenueSplit:
		var v ExtractedRevenue
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if len(v.Segments) == 0 {
			return errors.New("no revenue segments")
		}
	default:
		return fmt.Errorf("unvalidatable segment %q", segment)
	}
	return nil
}
