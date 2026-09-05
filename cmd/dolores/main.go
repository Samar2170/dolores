// Command dolores is the CLI entry point. It exposes these subcommands:
//
//	api_data    fetch market API payloads (Alpha Vantage + IndiaSM) for a symbol
//	tickertape  extract stock data from a saved Tickertape page HTML
//	research    run the company research pipeline over the universe
//	key_metrics compute key metrics from stored tickertape financials
//	files       list or download a company's archived files from Archivus
//	financial_analysis  run the financial-analysis agent over stored data
//	business_competitive_analysis
//	                        run the business & competitive research agent over
//	                        the parsed annual report and investor presentation
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
	"dolores/internal/analysis"
	"dolores/internal/competitive"
	"dolores/internal/files"
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
	case "files":
		err = runCompanyFiles(ctx, os.Args[2:])
	case "financial_analysis":
		err = runFinancialAnalysis(ctx, os.Args[2:])
	case "business_competitive_analysis":
		err = runBusinessCompetitiveAnalysis(ctx, os.Args[2:])
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
  key_metrics   compute key metrics from stored tickertape financials into key_metrics
  files         list or download a company's archived files from Archivus
  financial_analysis
                run the financial-analysis agent over the stored data for a symbol
  business_competitive_analysis
                run the business & competitive research agent over the parsed
                annual report and investor presentation for a symbol`)
	os.Exit(2)
}

// runAPIData fetches the Alpha Vantage time series + global quote and the
// IndiaSM stock payload for a company by executing the fetcher tools
// (av_time_series_daily, av_global_quote, indiasm_stock), which archive and
// store each payload linked to the company's ID.
func runAPIData(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("api_data", flag.ExitOnError)
	symbol := fs.String("symbol", "", "stock symbol (required)")
	exchange := fs.String("exchange", "BSE", "stock exchange code (a stored company entry wins)")
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

	co, err := upsertCompany(st.DB, *symbol, *exchange)
	if err != nil {
		return err
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder())
	repo := market.NewRepo(st.DB).WithCompany(co.ID)

	reg := tool.NewRegistry()
	reg.Register(av.NewTimeSeriesDailyTool(av.New(arch, repo)))
	reg.Register(av.NewGlobalQuoteTool(av.New(arch, repo)))
	reg.Register(indiasm.NewStockTool(indiasm.New(arch, repo)))

	quoteArgs, err := json.Marshal(map[string]string{"symbol": co.Symbol, "exchange": co.Exchange})
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
// MongoDB, linked to the company's ID.
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

	co, err := upsertCompany(st.DB, *symbol, "")
	if err != nil {
		return err
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder())
	repo := market.NewRepo(st.DB).WithCompany(co.ID)

	return tickertape.ExtractToFile(ctx, co.Symbol, *htmlPath, *outPath, arch, repo)
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

// runFinancialAnalysis collects every stored data source for a symbol (the
// latest indiasm_stock payload, all key_metrics documents, the latest payload
// of every tickertape_* collection and the revenue_segmentation research
// resource when one exists) and runs the financial-analysis agent
// (prompts/financial_analysis.md) over it. The report is upserted into the
// company_financial_analysis collection and printed.
func runFinancialAnalysis(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("financial_analysis", flag.ExitOnError)
	symbol := fs.String("symbol", "", "stock symbol (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *symbol == "" {
		return fmt.Errorf("financial_analysis: -symbol is required")
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

	co, err := upsertCompany(st.DB, *symbol, "")
	if err != nil {
		return err
	}

	data, err := analysis.Collect(ctx, st.DB, co)
	if err != nil {
		return fmt.Errorf("financial_analysis: %w", err)
	}

	// newFile, err := os.Create("f.json")
	// if err != nil {
	// 	return err
	// }

	// defer newFile.Close()
	// err = json.NewEncoder(newFile).Encode(data)
	// if err != nil {
	// 	return err
	// }
	// return nil

	lg := llm.NewClient(fetcher_config.OPENROUTER_API_KEY, fetcher_config.Config.ALLOWED_MODELS,
		llm.WithTimeout(15*time.Minute))
	budget := llm.NewBudget(
		fetcher_config.ResearchLLMMaxRequests(),
		fetcher_config.ResearchLLMMaxTokens(),
	)
	started := time.Now()

	report, err := analysis.Run(llm.WithBudget(ctx, budget), lg, data)
	reqs, toks := budget.Snapshot()
	if err != nil {
		log.Printf("[financial_analysis] %s FAILED after %s: %v (llm: %d requests, %d tokens)",
			co.Symbol, time.Since(started).Round(time.Millisecond), err, reqs, toks)
		return err
	}

	day, serr := analysis.SaveReport(ctx, st.DB, data, report, reqs, toks)
	if serr != nil {
		log.Printf("[financial_analysis] %s: save report failed: %v", co.Symbol, serr)
	} else {
		log.Printf("[financial_analysis] %s: report saved to %s (day %s)",
			co.Symbol, models.ColCompanyFinancialAnalysis, day)
	}

	fmt.Println(report)
	log.Printf("[financial_analysis] %s completed in %s (llm: %d requests, %d tokens)",
		co.Symbol, time.Since(started).Round(time.Millisecond), reqs, toks)
	return nil
}

// runBusinessCompetitiveAnalysis loads the latest parsed annual report and
// parsed investor presentation of a company from Archivus into memory and
// runs the business & competitive research agent
// (prompts/business_competitive_analysis.md) over them. The report is
// upserted into the company_business_and_competitive_analysis collection,
// printed and optionally written to -out.
func runBusinessCompetitiveAnalysis(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("business_competitive_analysis", flag.ExitOnError)
	symbol := fs.String("symbol", "", "stock symbol (required)")
	out := fs.String("out", "", "path to also write the report to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *symbol == "" {
		return fmt.Errorf("business_competitive_analysis: -symbol is required")
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
	if err := competitive.Migrate(st.DB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	co, err := upsertCompany(st.DB, *symbol, "")
	if err != nil {
		return err
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder()).WithTimeout(5 * time.Minute)
	data, err := competitive.Collect(ctx, st.DB, arch, co)
	if err != nil {
		return fmt.Errorf("business_competitive_analysis: %w", err)
	}
	fmt.Println("[competitive] documents collected:")
	for kind, doc := range data.Documents {
		log.Printf("[competitive] %s: %s ready (%d/%d pages, %.1f KB text, truncated=%t) file=%s",
			co.Symbol, kind, doc.Pages, doc.TotalPages, float64(doc.Chars)/1024, doc.Truncated, doc.ParsedFileURL)

	}

	lg := llm.NewClient(fetcher_config.OPENROUTER_API_KEY, fetcher_config.Config.ALLOWED_MODELS,
		llm.WithTimeout(15*time.Minute))
	budget := llm.NewBudget(
		fetcher_config.ResearchLLMMaxRequests(),
		fetcher_config.ResearchLLMMaxTokens(),
	)
	started := time.Now()

	report, err := competitive.Run(llm.WithBudget(ctx, budget), lg, data)
	reqs, toks := budget.Snapshot()
	if err != nil {
		log.Printf("[competitive] %s FAILED after %s: %v (llm: %d requests, %d tokens)",
			co.Symbol, time.Since(started).Round(time.Millisecond), err, reqs, toks)
		return err
	}

	day, serr := competitive.SaveReport(ctx, st.DB, data, report, reqs, toks)
	if serr != nil {
		log.Printf("[competitive] %s: save report failed: %v", co.Symbol, serr)
	} else {
		log.Printf("[competitive] %s: report saved to %s (day %s)",
			co.Symbol, models.ColCompanyCompetitiveAnalysis, day)
	}

	if *out != "" {
		if werr := os.WriteFile(*out, []byte(report), 0o644); werr != nil {
			log.Printf("[competitive] %s: write report to %s failed: %v", co.Symbol, *out, werr)
		} else {
			log.Printf("[competitive] %s: report written to %s", co.Symbol, *out)
		}
	}

	fmt.Println(report)
	log.Printf("[competitive] %s completed in %s (llm: %d requests, %d tokens)",
		co.Symbol, time.Since(started).Round(time.Millisecond), reqs, toks)
	return nil
}

// runCompanyFiles lists or downloads a company's archived Archivus files by
// executing the archivus_company_files tool.
func runCompanyFiles(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	symbol := fs.String("symbol", "", "stock symbol (required)")
	folder := fs.String("folder", "", "subfolder below <SYMBOL>/ (e.g. research)")
	file := fs.String("file", "", "exact file name to download (default: list the folder)")
	out := fs.String("out", "", "download destination path (default: the file name)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *symbol == "" {
		return fmt.Errorf("files: -symbol is required")
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

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder()).WithTimeout(5 * time.Minute)
	toolArgs, err := json.Marshal(files.FilesArgs{
		Symbol: *symbol,
		Folder: *folder,
		File:   *file,
		Out:    *out,
	})
	if err != nil {
		return err
	}
	return files.NewCompanyFilesTool(arch, st.DB).Execute(ctx, toolArgs)
}

// runResearch runs the company research pipeline over the research universe,
// collecting documents and extracting topic segments.
func runResearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("research", flag.ExitOnError)
	symbolFilter := fs.String("symbol", "", "process only this symbol")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// Two-tier HTTP clients: patient for document downloads, snappy for
	// HTML/search/IR-path probes so a hanging site cannot stall the run.
	hcWeb := research.NewBrowserClient(45 * time.Second)
	hcDocs := research.NewBrowserClient(120 * time.Second)
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
	col := research.NewCollector(hcDocs, arch, dbStore.DB)

	if *symbolFilter == "" {
		return nil
	}
	fmt.Printf("\n########## %s ", *symbolFilter)
	budget := llm.NewBudget(
		fetcher_config.ResearchLLMMaxRequests(),
		fetcher_config.ResearchLLMMaxTokens(),
	)
	started := time.Now()
	// Hard ceiling for one symbol: discovery, downloads, parsing and all
	// LLM segments must finish inside this window.
	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	// Upsert the company first so every downstream write links to its ID.
	info, err := research.GetCompanyBySymbol(dbStore.DB, *symbolFilter)
	if err != nil {
		return fmt.Errorf("get company by symbol: %w", err)
	}
	if info == nil {
		return fmt.Errorf("research: %s not in companies universe (run importnifty first)", *symbolFilter)
	}
	co, err := research.UpsertCompany(dbStore.DB, *info)
	if err != nil {
		return fmt.Errorf("upsert company: %w", err)
	}
	err = runCompany(llm.WithBudget(ctx, budget), dbStore.DB, hcWeb, lg, col, co)
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

// upsertCompany resolves the company for symbol and upserts it into the
// companies collection before any other work. The stored research-universe
// entry wins when present; otherwise a minimal company is created from
// symbol+exchange (defaulting to BSE, as in the universe import). All
// downstream writes link to the returned company's ID.
func upsertCompany(db *mongo.Database, symbol, exchange string) (*models.Company, error) {
	info := research.CompanyInfo{Symbol: symbol, Exchange: exchange}
	found, err := research.GetCompanyBySymbol(db, symbol)
	if err != nil {
		return nil, fmt.Errorf("get company by symbol: %w", err)
	}
	if found != nil {
		info = *found
	} else if info.Exchange == "" {
		info.Exchange = "BSE"
	}
	co, err := research.UpsertCompany(db, info)
	if err != nil {
		return nil, fmt.Errorf("upsert company: %w", err)
	}
	log.Printf("[company] %s (%s) linked as %s", co.Symbol, co.Exchange, co.ID.Hex())
	return co, nil
}

func runCompany(ctx context.Context, db *mongo.Database, hc *http.Client, lg *llm.Client, col *research.Collector, co *models.Company) error {
	info := research.InfoFromCompany(co)

	log.Printf("[company] %s: discovery started", co.Symbol)
	disc, err := research.DiscoverCompany(ctx, hc, info)
	if err != nil {
		return fmt.Errorf("discovery: %w", err)
	}
	if err := research.SaveLinks(db, co.ID, disc.OfficialWebsite, disc.IRLink); err != nil {
		log.Printf("[company] %s: links save failed: %v", co.Symbol, err)
	}

	log.Printf("[company] %s: collecting documents", co.Symbol)
	docs, err := col.Collect(ctx, co.ID, info, disc)
	if err != nil {
		log.Printf("[company] %s: collection failed: %v", co.Symbol, err)
	}
	logSummary(co, docs)

	sources := docSources(docs)

	for _, segment := range topicSegments {
		if llm.Exhausted(ctx) {
			log.Printf("[company] %s: llm budget exhausted - skipping remaining segments", co.Symbol)
			break
		}
		log.Printf("[company] %s: extracting %s", co.Symbol, segment)
		in := research.ResourceInput{
			CompanyID:    co.ID,
			Segment:      segment,
			Source:       truncate(sources.labels, maxSourceLabel),
			ResearchLink: sources.urls,
			RawDataUrl:   sources.rawURL,
		}
		row, err := research.UpsertResourcePending(db, in)
		if err != nil {
			log.Printf("[company] %s: row upsert (%s) failed: %v", co.Symbol, segment, err)
			continue
		}

		payload, xerr := research.ExtractTopics(ctx, lg, docs, segment)
		switch {
		case xerr == research.ErrNoText:
			if len(docs) == 0 {
				log.Printf("[company] %s: no documents archived - %s left pending", co.Symbol, segment)
			} else {
				log.Printf("[company] %s: no text layer for %s (scanned docs?) - left pending", co.Symbol, segment)
			}
		case xerr != nil:
			log.Printf("[company] %s: extraction (%s) failed: %v", co.Symbol, segment, xerr)
		default:
			if serr := research.SetExtractedData(db, row.ID, payload); serr != nil {
				log.Printf("[company] %s: storing extraction (%s) failed: %v", co.Symbol, segment, serr)
			} else {
				log.Printf("[company] %s: extracted %s OK", co.Symbol, segment)
			}
		}
	}

	return reviewRowState(db, co)
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
		if d.Kind == "ir_index_page" {
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

func logSummary(co *models.Company, docs []research.CollectedDoc) {
	for _, d := range docs {
		rawURL := d.RawDataUrl
		if len(rawURL) > 60 {
			rawURL = rawURL[:57] + "..."
		}
		log.Printf("[archive] %-9s %-26s %8d bytes -> %s (signed=%t)",
			co.Symbol, d.FileName, len(d.Bytes), rawURL, d.RawDataUrl != "")
	}
}

func reviewRowState(db *mongo.Database, co *models.Company) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Printf("\n--- %s resource rows ---\n", co.Symbol)
	cur, err := db.Collection(models.ColCompanyResources).Find(ctx,
		bson.M{"company_id": co.ID},
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
