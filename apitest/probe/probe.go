package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	fetcher_config "dolores/fetcher/config"
	"dolores/internal/llm"
	"dolores/internal/research"
	"dolores/storage"
)

const (
	welcorpAR   = "https://www.welspuncorp.com/uploads/investor_data/investorreport_Financial%20Year%202025%20-%202026_1527.pdf"
	welcorpPres = "https://www.welspuncorp.com/uploads/investor_data/investorreport__1557.pdf"
	hdfcAR      = "https://www.hdfc.bank.in/content/dam/hdfcbankpws/in/en/pdf/annual-reports/2024-25/HDFC_Bank_Annual_Report_2024_25-310202.pdf"
)

type jsonProbe struct {
	Company      string   `json:"company"`
	SegmentCount int      `json:"segment_count"`
	Segments     []string `json:"segments"`
}

func main() {
	which := flag.String("probe", "all", "llm | pdf | upload | all")
	flag.Parse()
	ctx := context.Background()

	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		log.Fatalf("config: %v", err)
	}

	run := map[string]func(context.Context){
		"llm":    probeLLM,
		"pdf":    probePDF,
		"upload": probeUpload,
	}
	if *which == "all" {
		for _, name := range []string{"llm", "pdf", "upload"} {
			fmt.Printf("\n========== PROBE: %s ==========\n", strings.ToUpper(name))
			run[name](ctx)
		}
		return
	}
	fn, ok := run[*which]
	if !ok {
		log.Fatalf("unknown probe %q", *which)
	}
	fn(ctx)
}

func probeLLM(ctx context.Context) {
	client := llm.NewClient(fetcher_config.OPENROUTER_API_KEY, fetcher_config.Config.ALLOWED_MODELS)
	start := time.Now()
	raw, err := client.CompleteJSON(ctx,
		"You return ONLY valid minified JSON, no prose, no markdown fences.",
		`An analyst studies a company that makes these three segments: FMCG, Paperboards, Agri. Using this example, respond with JSON exactly shaped as {"company":"ITC","segment_count":<number>,"segments":[names]} for company "ITC".`)
	if err != nil {
		log.Printf("[llm] FAILED: %v", err)
		return
	}
	var out jsonProbe
	jerr := json.Unmarshal(raw, &out)
	log.Printf("[llm] ok in %s; parse=%v raw=%s", time.Since(start).Round(time.Millisecond), jerr == nil, truncate(string(raw), 300))
}

func probePDF(ctx context.Context) {
	for _, url := range []struct{ label, url string }{
		{"WCL-AR-scanned", welcorpAR},
		{"WCL-presentation", welcorpPres},
	} {
		log.Printf("[pdf] --- %s ---", url.label)
		extractAndReport(ctx, url.url)
	}
}

func extractAndReport(ctx context.Context, url string) {
	start := time.Now()
	data, ctype, err := research.FetchHTTP(ctx, research.NewBrowserClient(2*time.Minute), url, 80<<20)
	if err != nil {
		log.Fatalf("[pdf] download failed: %v", err)
	}
	log.Printf("[pdf] downloaded %d bytes (%s) in %s; magic=%v",
		len(data), ctype, time.Since(start).Round(time.Millisecond), research.IsPDF(data))

	pages, total, issues, err := research.ExtractPDFPages(data)
	if err != nil {
		log.Printf("[pdf] extraction failed: %v", err)
		return
	}
	extractedChars := 0
	for _, p := range pages {
		extractedChars += len(p.Text)
	}
	log.Printf("[pdf] pages extracted %d/%d (~%.0f%%), %d chars, issues=%d",
		len(pages), total,
		func() float64 {
			if total == 0 {
				return 0
			}
			return float64(len(pages)) / float64(total) * 100
		}(), extractedChars, len(issues))
	if len(issues) > 0 && len(issues) < 5 {
		for _, is := range issues {
			log.Printf("[pdf] issue: %s", is)
		}
	}

	if len(pages) == 0 {
		return
	}
	first := strings.Join(strings.Fields(pages[0].Text), " ")
	log.Printf("[pdf] sample p%d: %s", pages[0].Page, truncate(first, 400))

	picked := research.SelectPages(pages,
		[]string{"raw material", "product", "segment", "revenue"}, 6000)
	log.Printf("[pdf] keyword picker returned %d chars; head:\n%s", len(picked), truncate(picked, 700))
}

func probeUpload(ctx context.Context) {
	start := time.Now()
	data, ctype, err := research.FetchHTTP(ctx, research.NewBrowserClient(3*time.Minute), hdfcAR, 90<<20)
	if err != nil {
		log.Fatalf("[upload] download failed: %v", err)
	}
	dl := time.Since(start)
	log.Printf("[upload] downloaded HDFC AR %.1f MB (%s) in %s -> %.1f MB/s",
		float64(len(data))/(1<<20), ctype, dl.Round(time.Millisecond), float64(len(data))/(1<<20)/dl.Seconds())
	if !research.IsPDF(data) {
		log.Fatalf("[upload] not a PDF")
	}
	if pages, total, _, err := research.ExtractPDFPages(data); err == nil {
		log.Printf("[upload] HDFC AR text layer: %d/%d pages", len(pages), total)
	}

	client := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder()).WithTimeout(10 * time.Minute)

	ls, err := client.List("")
	if err != nil {
		log.Fatalf("[upload] root list failed: %v", err)
	}
	for _, f := range ls {
		log.Printf("[upload] pre-existing entry: name=%s isDir=%v ext=%s size=%.1fKB",
			f.Name, f.IsDir, f.Extension, f.Size/1024)
	}

	start = time.Now()
	err = client.Upload("research_probe", []*storage.UploadFile{{
		Name:    "HDFCBANK_integrated_annual_report_FY2025_probe.pdf",
		Content: data,
	}})
	up := time.Since(start)
	if err != nil {
		log.Fatalf("[upload] FAILED after %s: %v", up.Round(time.Millisecond), err)
	}
	log.Printf("[upload] uploaded %.1f MB in %s -> %.1f MB/s",
		float64(len(data))/(1<<20), up.Round(time.Millisecond), float64(len(data))/(1<<20)/up.Seconds())

	files, err := client.List("research_probe")
	if err != nil {
		log.Fatalf("[upload] verify list failed: %v", err)
	}
	for _, f := range files {
		log.Printf("[upload] verified: name=%s id=%s size=%.1fMB signedUrl=%t",
			f.Name, f.ID, f.Size/(1<<20), f.SignedURL != "")
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...<truncated>"
}
