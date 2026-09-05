// Package competitive runs the business & competitive research agent over a
// company's parsed annual report and parsed investor presentation: the parsed
// text files are pulled from Archivus into memory and handed to the agent
// driven by prompts/business_competitive_analysis.md.
package competitive

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
	"dolores/storage"
)

// Doc kinds stored in company_research_resources_parsed.
const (
	DocKindAnnualReport         = "annual_report"
	DocKindInvestorPresentation = "investor_presentation"
)

// perDocCharBudget caps the text handed to the agent per document; oversize
// documents keep their head (business overview, segments, MD&A) and tail
// (financial statements, notes).
const perDocCharBudget = 200_000

// Document is one parsed source document loaded into memory from Archivus.
type Document struct {
	Kind          string `json:"kind"`
	Title         string `json:"title,omitempty"`
	FY            string `json:"fy,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	ParsedFile    string `json:"parsed_file,omitempty"`
	ParsedFileURL string `json:"parsed_file_url,omitempty"`
	Pages         int    `json:"pages"`
	TotalPages    int    `json:"total_pages,omitempty"`
	Chars         int    `json:"chars"`
	Truncated     bool   `json:"truncated,omitempty"`
	Text          string `json:"text"`
}

// companyProfile ties the analysis back to the company_id.
type companyProfile struct {
	CompanyID bson.ObjectID `json:"company_id"`
	Symbol    string        `json:"symbol"`
	Exchange  string        `json:"exchange,omitempty"`
	Name      string        `json:"name,omitempty"`
	Industry  string        `json:"industry,omitempty"`
	ISINCode  string        `json:"isin_code,omitempty"`
}

// Data is the agent input: company identity plus every parsed document that
// could be loaded, keyed by document kind.
type Data struct {
	Symbol    string               `json:"symbol"`
	Company   companyProfile       `json:"company"`
	Documents map[string]*Document `json:"documents"`
}

// Collect loads the latest parsed annual report and the latest parsed
// investor presentation for the company from Archivus into memory. Sources
// that are missing or unreadable are skipped with a log line; the error is
// non-nil on database failures or when no parsed document is available at all.
func Collect(ctx context.Context, db *mongo.Database, arch *storage.ArchivusClient, co *models.Company) (*Data, error) {
	d := &Data{
		Symbol: co.Symbol,
		Company: companyProfile{
			CompanyID: co.ID,
			Symbol:    co.Symbol,
			Exchange:  co.Exchange,
			Name:      co.Name,
			Industry:  co.Industry,
			ISINCode:  co.ISINCode,
		},
		Documents: map[string]*Document{},
	}

	for _, kind := range []string{DocKindAnnualReport, DocKindInvestorPresentation} {
		row, err := latestParsed(ctx, db, co.ID, kind)
		if err != nil {
			return nil, err
		}
		if row == nil {
			log.Printf("[competitive] %s: no parsed %s archived", co.Symbol, kind)
			continue
		}
		doc, err := loadDoc(ctx, db, arch, co, row)
		if err != nil {
			log.Printf("[competitive] %s: load parsed %s failed: %v - skipping", co.Symbol, kind, err)
			continue
		}
		d.Documents[kind] = doc
		log.Printf("[competitive] %s: %s ready (%d/%d pages, %.1f KB text, truncated=%t)",
			co.Symbol, kind, doc.Pages, doc.TotalPages, float64(doc.Chars)/1024, doc.Truncated)
	}

	if len(d.Documents) == 0 {
		return nil, fmt.Errorf("competitive: no parsed annual report or investor presentation for %s (run research first)", co.Symbol)
	}
	return d, nil
}

// latestParsed fetches the most recently refreshed parsed-resource row of one
// kind; nil when none is stored.
func latestParsed(ctx context.Context, db *mongo.Database, companyID bson.ObjectID, kind string) (*models.ParsedResource, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var r models.ParsedResource
	err := db.Collection(models.ColParsedResources).FindOne(ctx,
		bson.M{"company_id": companyID, "kind": kind},
		options.FindOne().SetSort(bson.D{{Key: "updated_at", Value: -1}, {Key: "fy", Value: -1}}),
	).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", models.ColParsedResources, err)
	}
	return &r, nil
}

// loadDoc downloads one parsed text file from Archivus and enriches it with
// the source-document metadata recorded in analysis_files.
func loadDoc(ctx context.Context, db *mongo.Database, arch *storage.ArchivusClient, co *models.Company, row *models.ParsedResource) (*Document, error) {
	folder := path.Dir("/" + row.ArchivusPath)
	entries, err := arch.List(folder)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", folder, err)
	}

	var fi *storage.FileInfo
	for i := range entries {
		if !entries[i].IsDir && strings.EqualFold(entries[i].Name, row.ParsedFile) {
			fi = &entries[i]
			break
		}
	}
	if fi == nil {
		return nil, fmt.Errorf("no file %q in %s", row.ParsedFile, folder)
	}

	data, _, err := arch.Download(fi.ID)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", row.ParsedFile, err)
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("%s is empty", row.ParsedFile)
	}

	doc := &Document{
		Kind:          row.Kind,
		FY:            row.FY,
		ParsedFile:    row.ParsedFile,
		ParsedFileURL: fi.SignedURL,
		Pages:         row.Pages,
		TotalPages:    row.TotalPages,
		Chars:         len(text),
	}
	doc.Title, doc.SourceURL = sourceMeta(ctx, db, co.ID, row.SourceFile)
	doc.Text, doc.Truncated = truncateMiddle(text, perDocCharBudget)
	return doc, nil
}

// sourceMeta looks up the original source URL and title of the raw document
// behind a parsed text file (best effort; empty strings when unrecorded).
func sourceMeta(ctx context.Context, db *mongo.Database, companyID bson.ObjectID, sourceFile string) (title, sourceURL string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var f models.AnalysisFile
	err := db.Collection(models.ColAnalysisFiles).FindOne(ctx,
		bson.M{"company_id": companyID, "file_name": sourceFile},
	).Decode(&f)
	if err != nil {
		return "", ""
	}
	return f.Title, f.SourceURL
}

// truncateMiddle cuts an oversize text down to budget bytes, keeping the head
// and the tail with a truncation marker in between.
func truncateMiddle(s string, budget int) (string, bool) {
	if budget <= 0 || len(s) <= budget {
		return s, false
	}
	head := budget * 2 / 3
	for head < len(s) && (s[head]&0xC0) == 0x80 {
		head++
	}
	tailStart := len(s) - (budget - head)
	for tailStart > head && (s[tailStart]&0xC0) == 0x80 {
		tailStart--
	}
	marker := fmt.Sprintf("\n\n[... %d characters truncated ...]\n\n", tailStart-head)
	return s[:head] + marker + s[tailStart:], true
}
