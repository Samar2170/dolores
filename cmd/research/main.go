package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	fetcher_config "dolores/fetcher/config"
	"dolores/internal/llm"
	"dolores/internal/models"
	"dolores/internal/research"
	"dolores/internal/store"
	"dolores/storage"
)

var topicSegments = []string{
	models.SegmentProducts,
	models.SegmentRawInputs,
	models.SegmentRevenueSplit,
}

const maxSourceLabel = 240

func main() {
	symbolFilter := flag.String("symbol", "", "process only this symbol")
	flag.Parse()

	ctx := context.Background()
	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		log.Fatalf("config: %v", err)
	}

	hc := research.NewBrowserClient(90 * time.Second)
	arch := storage.NewArchivusClient(
		fetcher_config.ARCHIVUS_API_KEY,
		fetcher_config.StorageParentFolder(),
	).WithTimeout(10 * time.Minute)

	dbStore, err := store.GetStore(".")
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	if err := research.MigrateCompanyModels(dbStore.DB); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	lg := llm.NewClient(fetcher_config.OPENROUTER_API_KEY, fetcher_config.Config.ALLOWED_MODELS)
	col := research.NewCollector(hc, arch)

	for _, info := range research.Universe {
		if *symbolFilter != "" && info.Symbol != *symbolFilter {
			continue
		}
		fmt.Printf("\n########## %s (%s) ##########\n", info.Symbol, info.Name)
		started := time.Now()
		err := runCompany(ctx, dbStore.DB, hc, arch, lg, col, info)
		if err != nil {
			log.Printf("[company] %s FAILED after %s: %v",
				info.Symbol, time.Since(started).Round(time.Millisecond), err)
			continue
		}
		log.Printf("[company] %s completed in %s", info.Symbol, time.Since(started).Round(time.Millisecond))
	}
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
