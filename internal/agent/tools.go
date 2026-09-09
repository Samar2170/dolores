package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"dolores/internal/research"
	"dolores/internal/storage"
)

const (
	maxFetchBytes   = 30 << 20
	defaultMaxChars = 20000
)

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	return unsafeNameRe.ReplaceAllString(strings.TrimSpace(s), "_")
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(sanitizeName(symbol))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clampChars(n int) int {
	switch {
	case n <= 0:
		return defaultMaxChars
	case n < 2000:
		return 2000
	case n > 60000:
		return 60000
	default:
		return n
	}
}

func archiveFolder(symbol, sub string) (string, error) {
	sub = strings.Trim(strings.TrimSpace(sub), "/")
	if strings.Contains(sub, "..") {
		return "", fmt.Errorf("folder must stay inside <SYMBOL>/")
	}
	root := normalizeSymbol(symbol)
	if sub == "" || sub == "." {
		return root, nil
	}
	return root + "/" + sub, nil
}

// pagesToText renders page texts, keyword-ranked when keywords are given and
// sequential otherwise, capped at maxChars with [page N] markers kept intact.
func pagesToText(pages []research.PageText, keywords []string, maxChars int) string {
	if len(keywords) > 0 {
		if sel := research.SelectPages(pages, keywords, maxChars); strings.TrimSpace(sel) != "" {
			return sel
		}
	}
	var sb strings.Builder
	for _, p := range pages {
		if sb.Len() >= maxChars {
			break
		}
		t := p.Text
		if sb.Len()+len(t) > maxChars {
			t = t[:maxChars-sb.Len()]
		}
		fmt.Fprintf(&sb, "[page %d]\n%s\n\n", p.Page, t)
	}
	return sb.String()
}

func pdfText(data []byte, keywords []string, maxChars int) (string, int, int, error) {
	pages, total, _, err := research.ExtractPDFPages(data)
	if err != nil {
		return "", 0, 0, err
	}
	if len(pages) == 0 {
		return "", 0, total, fmt.Errorf("pdf has no text layer (likely scanned)")
	}
	return pagesToText(pages, keywords, maxChars), len(pages), total, nil
}

var junkTags = map[string]bool{"script": true, "style": true, "noscript": true, "svg": true}

func htmlText(body string) string {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && junkTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			sb.WriteByte(' ')
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(sb.String(), " "))
}

type searchHit struct{ Title, URL string }

const ddgEndpoint = "https://html.duckduckgo.com/html/?q="

func ddgSearch(ctx context.Context, hc *http.Client, query string) ([]searchHit, error) {
	body, ctype, err := research.FetchHTTP(ctx, hc, ddgEndpoint+url.QueryEscape(query), 4<<20)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if !strings.Contains(ctype, "text/html") {
		return nil, fmt.Errorf("search: non-html response (%s)", ctype)
	}
	low := strings.ToLower(string(body))
	if strings.Contains(low, "anomaly") || strings.Contains(low, "captcha") {
		return nil, fmt.Errorf("search: challenge page (rate limited), retry later")
	}
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	var out []searchHit
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := ""
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					href = attr.Val
					break
				}
			}
			if strings.Contains(href, "uddg=") {
				if u, perr := url.Parse(href); perr == nil && u.Query().Get("uddg") != "" {
					out = append(out, searchHit{Title: nodeText(n), URL: u.Query().Get("uddg")})
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if len(out) > 12 {
		out = out[:12]
	}
	return out, nil
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
		}
		for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// ArchiveListTool lists entries of a company's Archivus archive folders.
type ArchiveListTool struct {
	arch   *storage.ArchivusClient
	symbol string
}

func NewArchiveListTool(arch *storage.ArchivusClient, symbol string) *ArchiveListTool {
	return &ArchiveListTool{arch: arch, symbol: symbol}
}

func (t *ArchiveListTool) Name() string { return "archivus_list" }

func (t *ArchiveListTool) Description() string {
	return `List entries of a company's Archivus archive folder. Args: {"symbol":"ITC","folder":"research"} - folder defaults to the symbol root (market payload JSONs); use "research" for annual reports, investor presentations and their parsed "<file>_text.txt" texts. Returns names, sizes and whether parsed text is available.`
}

type archiveListArgs struct {
	Symbol string `json:"symbol"`
	Folder string `json:"folder"`
}

func (t *ArchiveListTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a archiveListArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	sym := firstNonEmpty(a.Symbol, t.symbol)
	if sym == "" {
		return "", fmt.Errorf("symbol is required (pass -symbol or set args.symbol)")
	}
	folder, err := archiveFolder(sym, a.Folder)
	if err != nil {
		return "", err
	}
	entries, err := t.arch.List(folder)
	if err != nil {
		return "", fmt.Errorf("list %s: %w", folder, err)
	}
	parsed := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir && strings.HasSuffix(strings.ToLower(e.Name), "_text.txt") {
			parsed[e.Name[:len(e.Name)-len("_text.txt")]] = true
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "folder %s: %d entries\n", folder, len(entries))
	for i, e := range entries {
		if i >= 200 {
			fmt.Fprintf(&sb, "... %d more entries not shown\n", len(entries)-i)
			break
		}
		kind, size := "dir ", ""
		if !e.IsDir {
			kind = "file"
			size = fmt.Sprintf(" %d bytes", int64(e.Size))
		}
		mark := ""
		if !e.IsDir && parsed[strings.TrimSuffix(e.Name, ".pdf")] {
			mark = " [parsed text available]"
		}
		fmt.Fprintf(&sb, "%s %s%s%s\n", kind, e.Name, size, mark)
	}
	return sb.String(), nil
}

// ArchiveReadTool downloads one archived file and returns its text: parsed
// "<file>_text.txt" siblings are preferred for PDFs and PDFs are parsed on
// the fly when no parsed text exists yet.
type ArchiveReadTool struct {
	arch   *storage.ArchivusClient
	symbol string
}

func NewArchiveReadTool(arch *storage.ArchivusClient, symbol string) *ArchiveReadTool {
	return &ArchiveReadTool{arch: arch, symbol: symbol}
}

func (t *ArchiveReadTool) Name() string { return "archivus_read" }

func (t *ArchiveReadTool) Description() string {
	return `Read one file from a company's Archivus archive as text. Args: {"symbol":"ITC","folder":"research","file":"ITC_annual_report_FY2026.pdf","keywords":["revenue","segment"],"max_chars":20000}. For PDFs the parsed "<file>_text.txt" sibling is served when present, otherwise the PDF is parsed on the fly; "keywords" ranks the most relevant pages, "max_chars" caps the output.`
}

type archiveReadArgs struct {
	Symbol   string   `json:"symbol"`
	Folder   string   `json:"folder"`
	File     string   `json:"file"`
	Keywords []string `json:"keywords"`
	MaxChars int      `json:"max_chars"`
}

func (t *ArchiveReadTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a archiveReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	a.File = strings.TrimSpace(a.File)
	if a.File == "" {
		return "", fmt.Errorf("file is required")
	}
	if strings.ContainsRune(a.File, '/') {
		return "", fmt.Errorf("file must be a plain name inside the folder")
	}
	sym := firstNonEmpty(a.Symbol, t.symbol)
	if sym == "" {
		return "", fmt.Errorf("symbol is required (pass -symbol or set args.symbol)")
	}
	folder, err := archiveFolder(sym, a.Folder)
	if err != nil {
		return "", err
	}
	entries, err := t.arch.List(folder)
	if err != nil {
		return "", fmt.Errorf("list %s: %w", folder, err)
	}
	var target *storage.FileInfo
	for i := range entries {
		if !entries[i].IsDir && strings.EqualFold(entries[i].Name, a.File) {
			target = &entries[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("no file %q in %s (use archivus_list)", a.File, folder)
	}
	data, _, err := t.arch.Download(target.ID)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", target.Name, err)
	}
	maxChars := clampChars(a.MaxChars)
	low := strings.ToLower(target.Name)
	if strings.HasSuffix(low, ".pdf") || research.IsPDF(data) {
		base := target.Name
		if strings.HasSuffix(low, ".pdf") {
			base = base[:len(base)-len(".pdf")]
		}
		parsedName := base + "_text.txt"
		for i := range entries {
			if !entries[i].IsDir && strings.EqualFold(entries[i].Name, parsedName) {
				if txt, _, derr := t.arch.Download(entries[i].ID); derr == nil && len(txt) > 0 {
					return fmt.Sprintf("SOURCE: %s/%s (parsed text of %s)\n\n%s", folder, parsedName, target.Name, clipText(string(txt), maxChars)), nil
				}
			}
		}
		text, withText, total, perr := pdfText(data, a.Keywords, maxChars)
		if perr != nil {
			return "", perr
		}
		return fmt.Sprintf("SOURCE: %s/%s (pdf, %d/%d pages with text)\n\n%s", folder, target.Name, withText, total, text), nil
	}
	if bytes.ContainsRune(data, 0) {
		return "", fmt.Errorf("%s is binary and not a pdf", target.Name)
	}
	return fmt.Sprintf("SOURCE: %s/%s\n\n%s", folder, target.Name, clipText(string(data), maxChars)), nil
}

// WebSearchTool searches the public web via the DuckDuckGo HTML endpoint.
type WebSearchTool struct {
	hc *http.Client
}

func NewWebSearchTool(hc *http.Client) *WebSearchTool {
	return &WebSearchTool{hc: hc}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return `Search the public web for clues or documents. Args: {"query":"<company> raw materials annual report pdf","max_results":8}. Returns a numbered list of titles with URLs; follow up with web_fetch on the promising ones.`
}

type webSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func (t *WebSearchTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a webSearchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	hits, err := ddgSearch(ctx, t.hc, a.Query)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "no results", nil
	}
	if a.MaxResults > 0 && a.MaxResults < len(hits) {
		hits = hits[:a.MaxResults]
	}
	var sb strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, h.Title, h.URL)
	}
	return sb.String(), nil
}

// WebFetchTool fetches a public URL, returns its text (HTML reduced to visible
// text, PDFs parsed) and can archive downloaded PDFs into the company's
// research folder for future runs.
type WebFetchTool struct {
	hc     *http.Client
	arch   *storage.ArchivusClient
	symbol string
}

func NewWebFetchTool(hc *http.Client, arch *storage.ArchivusClient, symbol string) *WebFetchTool {
	return &WebFetchTool{hc: hc, arch: arch, symbol: symbol}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return `Fetch a public URL and return its text; PDFs are parsed automatically. Args: {"url":"https://...","keywords":["segment"],"max_chars":20000,"archive":false} - archive=true stores a fetched PDF under <SYMBOL>/research as web_<name>.pdf for future runs.`
}

type webFetchArgs struct {
	URL      string   `json:"url"`
	Keywords []string `json:"keywords"`
	MaxChars int      `json:"max_chars"`
	Archive  bool     `json:"archive"`
}

func (t *WebFetchTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a webFetchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	a.URL = strings.TrimSpace(a.URL)
	if a.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	maxChars := clampChars(a.MaxChars)
	body, ctype, err := research.FetchHTTP(ctx, t.hc, a.URL, maxFetchBytes)
	if err != nil {
		return "", err
	}
	if research.IsPDF(body) {
		text, withText, total, perr := pdfText(body, a.Keywords, maxChars)
		if perr != nil {
			return "", perr
		}
		note := ""
		if a.Archive {
			note = t.archivePDF(a.URL, body)
		}
		return fmt.Sprintf("SOURCE: %s (pdf, %d/%d pages with text, content-type %s)%s\n\n%s", a.URL, withText, total, ctype, note, text), nil
	}
	text := htmlText(string(body))
	if strings.TrimSpace(text) == "" {
		text = string(body)
	}
	return fmt.Sprintf("SOURCE: %s (content-type %s)\n\n%s", a.URL, ctype, clipText(text, maxChars)), nil
}

func (t *WebFetchTool) archivePDF(rawURL string, body []byte) string {
	if t.arch == nil || strings.TrimSpace(t.symbol) == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	name := sanitizeName("web_"+strings.TrimSuffix(base, ".pdf")) + ".pdf"
	folder, err := archiveFolder(t.symbol, "research")
	if err != nil {
		return ""
	}
	if err := t.arch.Upload(folder, []*storage.UploadFile{{Name: name, Content: body}}); err != nil {
		log.Printf("[agent] web pdf archive upload failed: %v", err)
		return ""
	}
	return fmt.Sprintf("\n[archived to %s/%s]", folder, name)
}
