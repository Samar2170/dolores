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

	"golang.org/x/net/html"

	"dolores/storage"
)

const (
	docKindIRIndex     = "ir_index_page"
	docKindProductText = "product_pages_text"
	maxDocBytes        = 90 << 20
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
}

type Collector struct {
	hc    *http.Client
	store *storage.ArchivusClient
}

func NewCollector(hc *http.Client, store *storage.ArchivusClient) *Collector {
	return &Collector{hc: hc, store: store}
}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	return unsafeNameRe.ReplaceAllString(strings.TrimSpace(s), "_")
}

// Collect downloads the selected discovery candidates plus auxiliary material,
// uploads everything missing into <SYMBOL>/research/, and returns in-memory
// copies ready for extraction.
func (c *Collector) Collect(ctx context.Context, info CompanyInfo, disc *DiscoveryResult) ([]CollectedDoc, error) {
	const subFolder = "research"
	fullFolder := info.Symbol + "/" + subFolder

	existing := map[string]bool{}
	if files, err := c.store.List(fullFolder); err != nil {
		log.Printf("[collect] %s: list failed (%v), continuing", info.Symbol, err)
	} else {
		for _, f := range files {
			if !f.IsDir {
				existing[f.Name] = true
			}
		}
	}

	var out []CollectedDoc

	ar := pickBest(disc.Docs, docAnnualReport)
	var pres []DocCandidate
	lastFY := ""
	for _, d := range disc.Docs {
		if d.Kind != docInvestorPresentation || d.URL == "" {
			continue
		}
		if len(pres) > 0 && (d.FY == lastFY || d.Latest < pres[len(pres)-1].Latest-1) && len(pres) >= 1 {
			continue
		}
		pres = append(pres, d)
		lastFY = d.FY
		if len(pres) >= 2 {
			break
		}
	}

	queue := make([]DocCandidate, 0, 4)
	if ar.URL != "" {
		queue = append(queue, *ar)
	}
	queue = append(queue, pres...)

	for _, cand := range queue {
		start := time.Now()
		data, ctype, err := FetchHTTP(ctx, c.hc, cand.URL, maxDocBytes)
		if err != nil {
			log.Printf("[collect] %s: FAILED %s: %v", info.Symbol, cand.URL, err)
			continue
		}
		if !IsPDF(data) && strings.Contains(ctype, "text/html") {
			log.Printf("[collect] %s: got html instead of pdf, skipping %s", info.Symbol, cand.URL)
			continue
		}
		fy := cand.FY
		if fy == "" {
			fy = time.Now().Format("2006")
		}
		name := sanitizeName(fmt.Sprintf("%s_%s_%s.pdf", info.Symbol, cand.Kind, fy))
		path := fullFolder + "/" + name
		if _, dup := existing[name]; !dup {
			upStart := time.Now()
			if err := c.store.Upload(fullFolder, []*storage.UploadFile{{Name: name, Content: data}}); err != nil {
				log.Printf("[collect] %s: upload failed %s: %v", info.Symbol, name, err)
				continue
			}
			log.Printf("[collect] %s: uploaded %s (%.1f MB, dl %.1fs, ul %.1fs)",
				info.Symbol, name, float64(len(data))/(1<<20),
				time.Since(start).Seconds(), time.Since(upStart).Seconds())
			existing[name] = true
		} else {
			log.Printf("[collect] %s: already archived, reused %s", info.Symbol, name)
		}
		out = append(out, CollectedDoc{
			Kind: cand.Kind, Title: pickTitle(cand), SourceURL: cand.URL, FY: cand.FY,
			FileName: name, ArchivusPath: path, Bytes: data,
		})
	}

	out = append(out, c.collectIRIndex(ctx, info, disc, fullFolder, existing)...)

	if info.Manufacturing() {
		if pt, err := c.collectProductPages(ctx, info, disc, fullFolder, existing); err != nil {
			log.Printf("[collect] %s: product pages: %v", info.Symbol, err)
		} else if pt != nil {
			out = append(out, *pt)
		}
	}

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
	}
	return out, nil
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

func pickBest(docs []DocCandidate, kind string) *DocCandidate {
	for i := range docs {
		if docs[i].Kind == kind && docs[i].Latest > 0 {
			return &docs[i]
		}
	}
	for i := range docs {
		if docs[i].Kind == kind {
			return &docs[i]
		}
	}
	return &DocCandidate{}
}

func (c *Collector) collectIRIndex(ctx context.Context, info CompanyInfo, disc *DiscoveryResult, fullFolder string, existing map[string]bool) []CollectedDoc {
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

func (c *Collector) collectProductPages(ctx context.Context, info CompanyInfo, disc *DiscoveryResult, fullFolder string, existing map[string]bool) (*CollectedDoc, error) {
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
