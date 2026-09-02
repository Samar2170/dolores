// Command dolores is the CLI entry point. It exposes three subcommands:
//
//	api_data    fetch market API payloads (Alpha Vantage + IndiaSM) for a symbol
//	tickertape  extract stock data from a saved Tickertape page HTML
//	research    run the company research pipeline over the universe
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/fetcher/av"
	fetcher_config "dolores/fetcher/config"
	"dolores/fetcher/indiasm"
	"dolores/fetcher/tickertape"
	"dolores/internal/llm"
	"dolores/internal/market"
	"dolores/internal/metrics"
	"dolores/internal/models"
	"dolores/internal/research"
	"dolores/internal/store"
	"dolores/internal/tool"
	"dolores/storage"
)

var topicSegments = []string{
	models.SegmentProducts,
	models.SegmentRawInputs,
	models.SegmentRevenueSplit,
}

const maxSourceLabel = 240

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()

	var err error
	switch os.Args[1] {
	case "api_data":
		err = runAPIData(ctx, os.Args[2:])
	case "tickertape":
		err = runTickertape(ctx, os.Args[2:])
	case "research":
		err = runResearch(ctx, os.Args[2:])
	case "key_metrics":
		err = runKeyMetrics(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `dolores - market data & research toolkit

usage: dolores <command> [flags]

commands:
  api_data      fetch market API payloads (Alpha Vantage + IndiaSM) for a symbol
  tickertape    extract stock data from a saved Tickertape page HTML
  research      run the company research pipeline (use -symbol to filter)
  key_metrics   compute key metrics from stored tickertape financials into key_metrics`)
	os.Exit(2)
}

// runAPIData fetches the Alpha Vantage time series + global quote and the
// IndiaSM stock payload for a symbol by executing the fetcher tools
// (av_time_series_daily, av_global_quote, indiasm_stock), which archive and
// store each payload.
func runAPIData(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("api_data", flag.ExitOnError)
	symbol := fs.String("symbol", "", "stock symbol (required)")
	exchange := fs.String("exchange", "BSE", "stock exchange code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *symbol == "" {
		return fmt.Errorf("api_data: -symbol is required")
	}

	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	st, err := store.GetStore(".")
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder())
	repo := market.NewRepo(st.DB)

	reg := tool.NewRegistry()
	reg.Register(av.NewTimeSeriesDailyTool(av.New(arch, repo)))
	reg.Register(av.NewGlobalQuoteTool(av.New(arch, repo)))
	reg.Register(indiasm.NewStockTool(indiasm.New(arch, repo)))

	quoteArgs, err := json.Marshal(map[string]string{"symbol": *symbol, "exchange": *exchange})
	if err != nil {
		return err
	}
	for _, name := range []string{av.ToolTimeSeriesDaily, av.ToolGlobalQuote, indiasm.ToolStock} {
		t, err := reg.Get(name)
		if err != nil {
			return err
		}
		if err := t.Execute(ctx, quoteArgs); err != nil {
			return err
		}
	}
	return nil
}

// runTickertape extracts stock data from a saved Tickertape stock page and
// writes it to a file, archives it to Archivus and stores each section in
// MongoDB.
func runTickertape(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tickertape", flag.ExitOnError)
	symbol := fs.String("symbol", "", "stock symbol (required)")
	htmlPath := fs.String("html", "", "path to the saved Tickertape page HTML (required)")
	outPath := fs.String("out", "", "output JSON path (default <symbol>_tickertape_extract.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *symbol == "" {
		return fmt.Errorf("tickertape: -symbol is required")
	}
	if *htmlPath == "" {
		return fmt.Errorf("tickertape: -html is required")
	}
	if *outPath == "" {
		*outPath = *symbol + "_tickertape_extract.json"
	}

	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	st, err := store.GetStore(".")
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder())
	repo := market.NewRepo(st.DB)

	return tickertape.ExtractToFile(ctx, *symbol, *htmlPath, *outPath, arch, repo)
}

// runKeyMetrics computes key metrics from the stored tickertape financial
// statements and upserts them into the key_metrics collection by executing
// the metrics_compute tool, for one symbol or for every symbol with
// financials.
func runKeyMetrics(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key_metrics", flag.ExitOnError)
	symbol := fs.String("symbol", "", "compute for one symbol (default: all symbols with tickertape financials)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.GetStore(".")
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	computeArgs, err := json.Marshal(metrics.ComputeArgs{Symbol: *symbol})
	if err != nil {
		return err
	}
	return metrics.NewComputeTool(st.DB).Execute(ctx, computeArgs)
}

// runResearch runs the company research pipeline over the research universe,
// collecting documents, extracting topic segments and market share data.
func runResearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("research", flag.ExitOnError)
	symbolFilter := fs.String("symbol", "", "process only this symbol")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	hc := research.NewBrowserClient(90 * time.Second)
	arch := storage.NewArchivusClient(
		fetcher_config.ARCHIVUS_API_KEY,
		fetcher_config.StorageParentFolder(),
	).WithTimeout(10 * time.Minute)

	dbStore, err := store.GetStore(".")
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if err := research.MigrateCompanyModels(dbStore.DB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	lg := llm.NewClient(fetcher_config.OPENROUTER_API_KEY, fetcher_config.Config.ALLOWED_MODELS)
	col := research.NewCollector(hc, arch)

	if *symbolFilter == "" {
		return nil
	}
	fmt.Printf("\n########## %s ", *symbolFilter)
	budget := llm.NewBudget(
		fetcher_config.ResearchLLMMaxRequests(),
		fetcher_config.ResearchLLMMaxTokens(),
	)
	started := time.Now()
	info, err := research.GetCompanyBySymbol(dbStore.DB, *symbolFilter)
	if err != nil {
		return fmt.Errorf("get company by symbol: %w", err)
	}
	err = runCompany(llm.WithBudget(ctx, budget), dbStore.DB, hc, arch, lg, col, *info)
	reqs, toks := budget.Snapshot()
	if err != nil {
		log.Printf("[company] %s FAILED after %s: %v (llm: %d requests, %d tokens)",
			info.Symbol, time.Since(started).Round(time.Millisecond), err, reqs, toks)
		return err
	}
	log.Printf("[company] %s completed in %s (llm: %d requests, %d tokens)",
		info.Symbol, time.Since(started).Round(time.Millisecond), reqs, toks)
	return nil
}

func runCompany(ctx context.Context, db *mongo.Database, hc *http.Client, arch *storage.ArchivusClient, lg *llm.Client, col *research.Collector, info research.CompanyInfo) error {
	co, err := research.UpsertCompany(db, info)
	if err != nil {
		return fmt.Errorf("upsert company: %w", err)
	}

	disc, err := research.DiscoverCompany(ctx, hc, info)
	if err != nil {
		return fmt.Errorf("discovery: %w", err)
	}
	if err := research.SaveLinks(db, co.ID, disc.OfficialWebsite, disc.IRLink); err != nil {
		log.Printf("[company] %s: links save failed: %v", info.Symbol, err)
	}

	docs, err := col.Collect(ctx, info, disc)
	if err != nil {
		log.Printf("[company] %s: collection failed: %v", info.Symbol, err)
	}
	logSummary(info, docs)

	sources := docSources(docs)

	for _, segment := range topicSegments {
		if llm.Exhausted(ctx) {
			log.Printf("[company] %s: llm budget exhausted - skipping remaining segments + market share", info.Symbol)
			break
		}
		in := research.ResourceInput{
			CompanyID:    co.ID,
			Segment:      segment,
			Source:       truncate(sources.labels, maxSourceLabel),
			ResearchLink: sources.urls,
			RawDataUrl:   sources.rawURL,
		}
		row, err := research.UpsertResourcePending(db, in)
		if err != nil {
			log.Printf("[company] %s: row upsert (%s) failed: %v", info.Symbol, segment, err)
			continue
		}

		payload, xerr := research.ExtractTopics(ctx, lg, docs, segment)
		switch {
		case xerr == research.ErrNoText:
			log.Printf("[company] %s: no text layer for %s (scanned docs?) - left pending", info.Symbol, segment)
		case xerr != nil:
			log.Printf("[company] %s: extraction (%s) failed: %v", info.Symbol, segment, xerr)
		default:
			if serr := research.SetExtractedData(db, row.ID, payload); serr != nil {
				log.Printf("[company] %s: storing extraction (%s) failed: %v", info.Symbol, segment, serr)
			} else {
				log.Printf("[company] %s: extracted %s OK", info.Symbol, segment)
			}
		}
	}

	if llm.Exhausted(ctx) {
		log.Printf("[company] %s: llm budget exhausted - skipping market share", info.Symbol)
		return reviewRowState(db, co.ID, info)
	}
	marketShare(ctx, db, lg, arch, info, co.ID)

	return reviewRowState(db, co.ID, info)
}

type sourceSummary struct {
	labels string
	urls   string
	rawURL string
}

func docSources(docs []research.CollectedDoc) sourceSummary {
	var names []string
	var urls []string
	raw := ""
	for _, d := range docs {
		if d.Kind == "ir_index_page" || d.Kind == "market_share_source" {
			continue
		}
		names = append(names, d.Title+" ["+d.Kind+"]")
		if len(urls) < 3 {
			urls = append(urls, d.SourceURL)
		}
		if raw == "" && d.RawDataUrl != "" {
			raw = d.RawDataUrl
		}
	}
	return sourceSummary{labels: strings.Join(names, "; "), urls: strings.Join(urls, "; "), rawURL: raw}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func logSummary(info research.CompanyInfo, docs []research.CollectedDoc) {
	for _, d := range docs {
		rawURL := d.RawDataUrl
		if len(rawURL) > 60 {
			rawURL = rawURL[:57] + "..."
		}
		log.Printf("[archive] %-9s %-26s %8d bytes -> %s (signed=%t)",
			info.Symbol, d.FileName, len(d.Bytes), rawURL, d.RawDataUrl != "")
	}
}

// marketShare runs the grounded market-share pass when product categories are
// already known from the products row.
func marketShare(ctx context.Context, db *mongo.Database, lg *llm.Client, arch *storage.ArchivusClient, info research.CompanyInfo, companyID bson.ObjectID) {
	payloadBytes, err := research.GetResourceJSON(db, companyID, models.SegmentProducts)
	if err != nil || payloadBytes == nil {
		log.Printf("[market] %s skipped: products not extracted yet", info.Symbol)
		return
	}
	var p research.ExtractedProducts
	if json.Unmarshal(payloadBytes, &p) != nil {
		log.Printf("[market] %s skipped: products payload unreadable", info.Symbol)
		return
	}
	var cats []string
	for _, c := range p.ProductCategories {
		if strings.TrimSpace(c.Name) != "" {
			cats = append(cats, c.Name)
		}
		if len(cats) >= 3 {
			break
		}
	}
	if len(cats) == 0 {
		log.Printf("[market] %s skipped: no categories in products payload", info.Symbol)
		return
	}

	payload, err := research.ResearchMarketShare(ctx, &http.Client{Timeout: 90 * time.Second}, arch, lg, info, cats)
	if err != nil {
		log.Printf("[market] %s failed: %v", info.Symbol, err)
		return
	}
	row, err := research.UpsertResourcePending(db, research.ResourceInput{
		CompanyID: companyID,
		Segment:   models.SegmentMarketShare,
		Source:    "LLM web research with cited sources (" + strings.Join(cats, ", ") + ")",
	})
	if err != nil {
		log.Printf("[market] %s: row upsert failed: %v", info.Symbol, err)
		return
	}
	if err := research.SetExtractedData(db, row.ID, payload); err != nil {
		log.Printf("[market] %s: storing market data failed: %v", info.Symbol, err)
		return
	}
	log.Printf("[market] %s stored", info.Symbol)
}

func reviewRowState(db *mongo.Database, companyID bson.ObjectID, info research.CompanyInfo) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Printf("\n--- %s resource rows ---\n", info.Symbol)
	cur, err := db.Collection(models.ColCompanyResources).Find(ctx,
		bson.M{"company_id": companyID},
		options.Find().SetSort(bson.M{"analysis_segment": 1}),
	)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	var rows []models.CompanyResearchResource
	if err := cur.All(ctx, &rows); err != nil {
		return err
	}
	for _, r := range rows {
		state := "pending"
		if r.ExtractedData != nil {
			state = fmt.Sprintf("JSON doc (%d keys)", len(r.ExtractedData))
		}
		link := r.ResearchResourceLink
		if len(link) > 80 {
			link = link[:77] + "..."
		}
		raw := r.RawDataUrl
		if len(raw) > 50 {
			raw = raw[:47] + "..."
		}
		fmt.Printf("  %-20s | %-14s | %s\n", r.AnalysisSegment, state, truncate(r.Source, 90))
		fmt.Printf("  %-20s | link: %s\n", "", link)
		fmt.Printf("  %-20s | raw : %t (archived)\n", "", raw != "")
	}
	return nil
}
