package research

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/net/html"

	"dolores/internal/storage"
)

const (
	docKindIRIndex     = "ir_index_page"
	docKindProductText = "product_pages_text"
	maxDocBytes        = 90 << 20

	maxAnnualReportAttempts = 3
	maxPresentationAttempts = 2
	minPDFBytes             = 20 << 10 // smaller files are usually error pages
)

// CollectedDoc is one raw material archived into the symbol's research folder.
type CollectedDoc struct {
	Kind         string
	Title        string
	SourceURL    string
	FY           string
	FileName     string
	ArchivusPath string
	RawDataUrl   string
	Bytes        []byte
	Pages        []PageText // PDF text layer, extracted once in Collect
	TotalPages   int
}

type Collector struct {
	hc    *http.Client
	store *storage.ArchivusClient
	db    *mongo.Database
}

func NewCollector(hc *http.Client, store *storage.ArchivusClient, db *mongo.Database) *Collector {
	return &Collector{hc: hc, store: store, db: db}
}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	return unsafeNameRe.ReplaceAllString(strings.TrimSpace(s), "_")
}

// Collect downloads the selected discovery candidates plus auxiliary material,
// uploads everything missing into <SYMBOL>/research/, records each file's
// metadata into the analysis_files collection, and returns in-memory copies
// ready for extraction. PDF text layers are extracted exactly once per run and
// archived as "<name>_text.txt" files, with metadata recorded in the
// company_research_resources_parsed collection.
func (c *Collector) Collect(ctx context.Context, companyID bson.ObjectID, info CompanyInfo, disc *DiscoveryResult) ([]CollectedDoc, error) {
	const subFolder = "research"
	fullFolder := info.Symbol + "/" + subFolder

	existing := map[string]storage.FileInfo{}
	if files, err := c.store.List(fullFolder); err != nil {
		log.Printf("[collect] %s: list failed (%v), continuing", info.Symbol, err)
	} else {
		for _, f := range files {
			if !f.IsDir {
				existing[f.Name] = f
			}
		}
	}

	var out []CollectedDoc
	var parsedRows []ParsedResourceInput

	// Ranked queues with fallback: the best-ranked candidate is attempted
	// first and the remaining ones only substitute for failed downloads.
	arQueue := candidatesOfKind(disc.Docs, docAnnualReport, maxAnnualReportAttempts)
	presQueue := candidatesOfKind(disc.Docs, docInvestorPresentation, maxPresentationAttempts+maxAnnualReportAttempts)

	successAR, successPres := 0, 0
	presFYs := map[string]bool{}
	queue := append(append(make([]DocCandidate, 0, len(arQueue)+len(presQueue)), arQueue...), presQueue...)
	for _, cand := range queue {
		isAR := cand.Kind == docAnnualReport
		if isAR {
			if successAR >= 1 {
				continue
			}
		} else {
			if successPres >= maxPresentationAttempts {
				continue
			}
			if cand.FY != "" && presFYs[cand.FY] {
				continue
			}
		}

		start := time.Now()
		data, ctype, name, reused, err := c.fetchDocBytes(ctx, info, existing, cand)
		if err != nil {
			log.Printf("[collect] %s: FAILED %s: %v", info.Symbol, cand.URL, err)
			continue
		}
		if !IsPDF(data) || len(data) < minPDFBytes {
			log.Printf("[collect] %s: not a usable pdf (%s, %d bytes), skipping %s",
				info.Symbol, ctype, len(data), cand.URL)
			continue
		}

		if !reused {
			if _, dup := existing[name]; !dup {
				if err := c.store.Upload(fullFolder, []*storage.UploadFile{{Name: name, Content: data}}); err != nil {
					log.Printf("[collect] %s: upload failed %s: %v", info.Symbol, name, err)
					continue
				}
				existing[name] = storage.FileInfo{Name: name}
			}
		}
		log.Printf("[collect] %s: %s ready (%.1f MB, %.1fs, reused=%t)",
			info.Symbol, name, float64(len(data))/(1<<20), time.Since(start).Seconds(), reused)

		doc := CollectedDoc{
			Kind: cand.Kind, Title: pickTitle(cand), SourceURL: cand.URL, FY: cand.FY,
			FileName: name, ArchivusPath: fullFolder + "/" + name, Bytes: data,
		}
		c.parseAndArchiveText(companyID, info, fullFolder, existing, &doc, &parsedRows)
		out = append(out, doc)

		if isAR {
			successAR++
		} else {
			successPres++
			if cand.FY != "" {
				presFYs[cand.FY] = true
			}
		}
	}

	out = append(out, c.collectIRIndex(ctx, info, disc, fullFolder, existing)...)

	if info.Manufacturing() {
		if pt, err := c.collectProductPages(ctx, info, disc, fullFolder, existing); err != nil {
			log.Printf("[collect] %s: product pages: %v", info.Symbol, err)
		} else if pt != nil {
			out = append(out, *pt)
		}
	}

	// Signed URLs for everything in the folder (raw files + parsed text).
	if files, err := c.store.List(fullFolder); err == nil {
		byName := map[string]storage.FileInfo{}
		for _, f := range files {
			byName[f.Name] = f
		}
		for i := range out {
			if fi, ok := byName[out[i].FileName]; ok && fi.SignedURL != "" {
				out[i].RawDataUrl = fi.SignedURL
			}
		}
		for i := range parsedRows {
			if fi, ok := byName[parsedRows[i].ParsedFile]; ok && fi.SignedURL != "" {
				parsedRows[i].RawDataUrl = fi.SignedURL
			}
		}
	}

	if err := SaveParsedResources(c.db, parsedRows); err != nil {
		log.Printf("[collect] %s: parsed resources save failed: %v", info.Symbol, err)
	}

	if c.db != nil {
		if err := SaveAnalysisFiles(c.db, companyID, info.Symbol, out); err != nil {
			log.Printf("[collect] %s: analysis_files save failed: %v", info.Symbol, err)
		}
	}
	return out, nil
}

// candidatesOfKind returns up to n ranked candidates of one kind, best first
// (dedupeAndRank has already ordered the discovery docs).
func candidatesOfKind(docs []DocCandidate, kind string, n int) []DocCandidate {
	out := make([]DocCandidate, 0, n)
	for _, d := range docs {
		if d.Kind != kind || d.URL == "" {
			continue
		}
		out = append(out, d)
		if len(out) >= n {
			break
		}
	}
	return out
}

// fetchDocBytes returns the PDF bytes of a candidate, reusing the copy already
// archived in Archivus when present and downloading from the source only when
// needed (re-runs keep working even when the original link has died).
func (c *Collector) fetchDocBytes(ctx context.Context, info CompanyInfo, existing map[string]storage.FileInfo, cand DocCandidate) (data []byte, ctype, name string, reused bool, err error) {
	fy := cand.FY
	if fy == "" {
		fy = time.Now().Format("2006")
	}
	name = sanitizeName(fmt.Sprintf("%s_%s_%s.pdf", info.Symbol, cand.Kind, fy))

	if fi, dup := existing[name]; dup {
		b, _, derr := c.store.Download(fi.ID)
		if derr == nil {
			return b, "", name, true, nil
		}
		log.Printf("[collect] %s: archived reuse of %s failed, re-downloading: %v", info.Symbol, name, derr)
	}

	b, ct, ferr := FetchHTTP(ctx, c.hc, cand.URL, maxDocBytes)
	if ferr != nil {
		return nil, "", name, false, ferr
	}
	return b, ct, name, false, nil
}

// parseAndArchiveText extracts the PDF text layer once, archives it as a
// sibling "<name>_text.txt" file, and queues a metadata row for the
// company_research_resources_parsed collection. Re-runs reuse the archived
// text instead of re-parsing the PDF.
func (c *Collector) parseAndArchiveText(companyID bson.ObjectID, info CompanyInfo, fullFolder string, existing map[string]storage.FileInfo, doc *CollectedDoc, rows *[]ParsedResourceInput) {
	base := strings.TrimSuffix(doc.FileName, ".pdf")
	parsedName := sanitizeName(base + "_text.txt")
	parsedPath := fullFolder + "/" + parsedName

	if fi, ok := existing[parsedName]; ok {
		data, _, err := c.store.Download(fi.ID)
		if err == nil {
			if pages := splitPageTexts(string(data)); len(pages) > 0 {
				doc.Pages = pages
				*rows = append(*rows, ParsedResourceInput{
					CompanyID: companyID, Symbol: info.Symbol, Kind: doc.Kind, FY: doc.FY,
					SourceFile: doc.FileName, ParsedFile: parsedName, ArchivusPath: parsedPath,
					Pages: len(pages), Chars: int64(len(data)),
				})
				log.Printf("[collect] %s: reused parsed text %s (%d pages)", info.Symbol, parsedName, len(pages))
				return
			}
		}
		log.Printf("[collect] %s: parsed-text reuse of %s failed, re-parsing: %v", info.Symbol, parsedName, err)
	}

	pages, total, issues, err := ExtractPDFPages(doc.Bytes)
	if err != nil {
		log.Printf("[collect] %s: %s pdf parse failed: %v", info.Symbol, doc.FileName, err)
		return
	}
	doc.TotalPages = total
	if len(pages) == 0 {
		log.Printf("[collect] %s: %s: 0/%d pages yielded text (likely scanned)", info.Symbol, doc.FileName, total)
		return
	}
	doc.Pages = pages

	content := []byte(joinPageTexts(pages))
	if _, dup := existing[parsedName]; !dup {
		if err := c.store.Upload(fullFolder, []*storage.UploadFile{{Name: parsedName, Content: content}}); err != nil {
			log.Printf("[collect] %s: parsed-text upload failed %s: %v", info.Symbol, parsedName, err)
			return
		}
		existing[parsedName] = storage.FileInfo{Name: parsedName}
	}
	log.Printf("[collect] %s: parsed %s (%d/%d pages, %.1f KB text, %d page issues)",
		info.Symbol, doc.FileName, len(pages), total, float64(len(content))/1024, len(issues))
	*rows = append(*rows, ParsedResourceInput{
		CompanyID: companyID, Symbol: info.Symbol, Kind: doc.Kind, FY: doc.FY,
		SourceFile: doc.FileName, ParsedFile: parsedName, ArchivusPath: parsedPath,
		Pages: len(pages), TotalPages: total, Chars: int64(len(content)),
	})
}

func pickTitle(c DocCandidate) string {
	t := cleanTitle(c.Title)
	if t == "" {
		return c.Kind + "_" + c.FY
	}
	if len(t) > 90 {
		t = t[:90]
	}
	return t
}

func (c *Collector) collectIRIndex(ctx context.Context, info CompanyInfo, disc *DiscoveryResult, fullFolder string, existing map[string]storage.FileInfo) []CollectedDoc {
	page := disc.IRLink
	if page == "" {
		page = disc.OfficialWebsite
	}
	body, ctype, err := FetchHTTP(ctx, c.hc, page, maxHTMLBytes)
	if err != nil {
		log.Printf("[collect] %s: ir index fetch failed: %v", info.Symbol, err)
		return nil
	}
	_ = ctype
	ext := "html"
	headEnd := len(body)
	if headEnd > 400 {
		headEnd = 400
	}
	head := strings.ToLower(string(body[:headEnd]))
	if !strings.Contains(head, "<html") && !strings.Contains(strings.ToLower(string(body)), "<body") {
		ext = "txt"
	}
	name := sanitizeName(fmt.Sprintf("%s_ir_index_%s.%s", info.Symbol, time.Now().Format("20060102"), ext))
	if _, dup := existing[name]; !dup {
		if err := c.store.Upload(fullFolder, []*storage.UploadFile{{Name: name, Content: body}}); err != nil {
			log.Printf("[collect] %s: ir index upload failed: %v", info.Symbol, err)
			return nil
		}
	}
	return []CollectedDoc{{
		Kind: docKindIRIndex, Title: "Investor relations index page",
		SourceURL: page, FileName: name, ArchivusPath: fullFolder + "/" + name, Bytes: body,
	}}
}

var productPageHints = []string{"products-and-services", "products-and-services/", "/products", "product.php", "-products"}

func (c *Collector) collectProductPages(ctx context.Context, info CompanyInfo, disc *DiscoveryResult, fullFolder string, existing map[string]storage.FileInfo) (*CollectedDoc, error) {
	base := disc.IRLink
	if base == "" {
		base = disc.OfficialWebsite
	}
	body, _, err := FetchHTTP(ctx, c.hc, base, maxHTMLBytes)
	if err != nil {
		return nil, err
	}
	bURL, _ := url.Parse(base)
	productRoot, _ := url.Parse(disc.OfficialWebsite)

	seen := map[string]bool{}
	var texts []string
	count := 0
	for _, a := range anchorsFromHTML(string(body)) {
		low := strings.ToLower(a.href)
		match := false
		for _, h := range productPageHints {
			if strings.Contains(low, h) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		full := resolveURL(bURL, a.href)
		if full == "" || seen[strings.ToLower(full)] {
			continue
		}
		seen[strings.ToLower(full)] = true

		pageBody, _, err := FetchHTTP(ctx, c.hc, full, 2<<20)
		if err != nil {
			continue
		}
		text := visibleText(string(pageBody))
		if len(text) < 120 {
			continue
		}
		texts = append(texts, fmt.Sprintf("=== %s ===\n%s", full, text))
		count++
		if count >= 8 {
			break
		}
	}
	_ = productRoot
	if count == 0 {
		return nil, fmt.Errorf("no usable product pages found")
	}
	content := []byte(strings.Join(texts, "\n\n"))
	name := sanitizeName(fmt.Sprintf("%s_product_pages.txt", info.Symbol))
	if _, dup := existing[name]; !dup {
		if err := c.store.Upload(fullFolder, []*storage.UploadFile{{Name: name, Content: content}}); err != nil {
			return nil, err
		}
	}
	return &CollectedDoc{
		Kind: docKindProductText, Title: "Official website product/service pages",
		SourceURL: disc.OfficialWebsite, FileName: name, ArchivusPath: fullFolder + "/" + name, Bytes: content,
	}, nil
}

var junkTags = map[string]bool{"script": true, "style": true, "noscript": true}

func visibleText(body string) string {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	var sb strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && junkTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			sb.WriteString(" ")
			sb.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(root)
	ws := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(ws.ReplaceAllString(sb.String(), " "))
}
