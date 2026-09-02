package research

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	docAnnualReport         = "annual_report"
	docInvestorPresentation = "investor_presentation"
	ddgEndpoint             = "https://html.duckduckgo.com/html/?q="
)

var blockedHostFragments = []string{
	"trendlyne.com", "scribd.com", "financialfilings.com", "publicnow.com",
	"wikipedia.org", "linkedin.com", "facebook.com", "twitter.com", "x.com",
	"youtube.com", "instagram.com", "whatsapp.com", "companiesmarketcap.com",
	"sustainabilityreports.com", "intracen.org", "stockssena.com", "kotakuat",
	"google.", "duckduckgo.com", "hdfcfund.com", "homeloans.hdfc",
	"welspuninvestments.com", "welspunliving.com", "welspun.com", "kidfl.kotak.com",
}

var irGuessPaths = []string{
	"/investor-relations", "/investors.html", "/investors", "/investor",
	"/investor-corner", "/investor-relations.html", "/ir",
	"/about-us/investor-relations", "/en/investor-relations.html",
}

// DocCandidate is a discovered document link on or near the official site.
type DocCandidate struct {
	URL    string
	Title  string
	Kind   string
	FY     string
	Source string
	Latest int
}

// DiscoveryResult groups everything discovery learned about a company.
type DiscoveryResult struct {
	OfficialWebsite string
	IRLink          string
	Docs            []DocCandidate
	Issues          []string
}

type searchHit struct {
	Title string
	URL   string
}

type anchor struct{ href, text string }

// DiscoverCompany runs the discovery pipeline for one company.
func DiscoverCompany(ctx context.Context, client *http.Client, info CompanyInfo) (*DiscoveryResult, error) {
	res := &DiscoveryResult{}

	hits := ddgSearch(ctx, client, fmt.Sprintf("%s %s investor relations annual report pdf", info.Name, info.Exchange))
	res.Issues = append(res.Issues, fmt.Sprintf("search hits=%d", len(hits)))

	official := pickOfficialHost(hits, info.Name)
	if official == "" {
		return res, fmt.Errorf("no official site found for %s (%s)", info.Name, info.Symbol)
	}
	res.OfficialWebsite = official
	log.Printf("[discover] %s official=%s", info.Symbol, official)

	res.IRLink = findIRLink(ctx, client, official)
	log.Printf("[discover] %s ir=%q", info.Symbol, res.IRLink)

	collectDocsFromPage(ctx, client, res)
	if countKind(res.Docs, docAnnualReport) == 0 {
		scanAnnualReportPages(ctx, client, officialURL(res), res)
	}
	if countKind(res.Docs, docAnnualReport) == 0 {
		for _, h := range ddgSearch(ctx, client, fmt.Sprintf("nsearchives.nseindia.com %s annual report pdf", info.Name)) {
			if !strings.Contains(h.URL, "nsearchives.nseindia.com") {
				continue
			}
			kind := classifyDoc(h.Title + " " + h.URL)
			if kind == "" {
				continue
			}
			addDoc(res, DocCandidate{URL: h.URL, Title: cleanTitle(h.Title), Kind: kind,
				FY: extractFY(h.Title + " " + h.URL), Source: "exchange_archive"})
		}
	}

	if countKind(res.Docs, docAnnualReport) == 0 || countKind(res.Docs, docInvestorPresentation) == 0 {
		for _, q := range []string{
			fmt.Sprintf("site:%s annual report filetype:pdf", hostOnly(official)),
			fmt.Sprintf("%s latest investor presentation filetype:pdf %s", info.Name, info.Exchange),
		} {
			for _, h := range ddgSearch(ctx, client, q) {
				kind := classifyDoc(h.Title + " " + h.URL)
				if kind == "" {
					continue
				}
				addDoc(res, DocCandidate{URL: h.URL, Title: cleanTitle(h.Title), Kind: kind,
					FY: extractFY(h.Title + " " + h.URL), Source: "search"})
			}
		}
	}
	dedupeAndRank(res)
	log.Printf("[discover] %s docs=%d", info.Symbol, len(res.Docs))
	for _, d := range res.Docs {
		log.Printf("[discover]   %-22s fy=%-10s src=%-13s %s", d.Kind, d.FY, d.Source, d.URL)
	}
	return res, nil
}

func ddgSearch(ctx context.Context, client *http.Client, query string) []searchHit {
	body, ctype, err := FetchHTTP(ctx, client, ddgEndpoint+url.QueryEscape(query), 4<<20)
	if err != nil {
		log.Printf("[discover] search failed for %q: %v", query, err)
		return nil
	}
	if !strings.Contains(ctype, "text/html") {
		log.Printf("[discover] search non-html response (%s) for %q", ctype, query)
		return nil
	}

	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil
	}
	var out []searchHit
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := ""
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
					break
				}
			}
			if strings.Contains(href, "uddg=") {
				u, err := url.Parse(href)
				if err == nil && u.Query().Get("uddg") != "" {
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

	if len(out) > 14 {
		out = out[:14]
	}
	return out
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

func pickOfficialHost(hits []searchHit, name string) string {
	tokens := nameTokens(name)
	best, bestScore, bestLen := "", -1, 1<<30
	for _, h := range hits {
		host := hostOnly(h.URL)
		if host == "" || isBlockedHost(host) {
			continue
		}
		score := 0
		for t := range tokens {
			if strings.Contains(host, t) {
				score++
			}
		}
		full := canonicalSiteURL(h.URL)
		if full == "" {
			continue
		}
		switch {
		case score > bestScore:
			best, bestScore, bestLen = full, score, len(host)
		case score == bestScore && score > 0 && len(host) < bestLen:
			best, bestScore, bestLen = full, score, len(host)
		}
	}
	return best
}

// canonicalSiteURL keeps the exact host seen in the winning search hit
// (including any www. prefix — apex domains of some Indian corporate sites
// present certificates that fail Go's verifier while www hosts do not).
func canonicalSiteURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return "https://" + strings.ToLower(u.Host)
}

var genericNameTokens = map[string]bool{
	"ltd": true, "limited": true, "the": true, "&": true, "inc": true,
	"group": true, "india": true, "co": true,
}

func nameTokens(name string) map[string]bool {
	fields := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '(' || r == ')'
	})
	set := make(map[string]bool)
	for _, f := range fields {
		if genericNameTokens[f] || len(f) < 2 {
			continue
		}
		set[f] = true
	}
	if len(set) == 0 {
		set[strings.ToLower(name)] = true
	}
	return set
}

func isBlockedHost(host string) bool {
	low := strings.ToLower(host)
	for _, b := range blockedHostFragments {
		if strings.Contains(low, b) {
			return true
		}
	}
	return false
}

func hostOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

// findIRLink fetches the homepage and looks for an investor link, falling back
// to conventional IR paths when navigation yields nothing useful.
func findIRLink(ctx context.Context, client *http.Client, official string) string {
	body, _, err := FetchHTTP(ctx, client, official, maxHTMLBytes)
	if err != nil {
		return guessIRPath(ctx, client, official)
	}
	base, _ := url.Parse(official)
	officialHost := strings.TrimPrefix(strings.ToLower(base.Host), "www.")
	best, bestDepth := "", 99
	for _, a := range anchorsFromHTML(string(body)) {
		href := strings.ToLower(a.href)
		text := strings.ToLower(a.text)
		isIR := strings.Contains(text, "investor") || strings.Contains(href, "investor") ||
			strings.Contains(text, "shareholder") || strings.Contains(href, "shareholder")
		if !isIR || strings.HasSuffix(href, ".pdf") {
			continue
		}
		full := resolveURL(base, a.href)
		if full == "" {
			continue
		}
		fullURL, err := url.Parse(full)
		if err != nil {
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(fullURL.Host), "www.")
		if host != officialHost && !strings.HasSuffix(host, "."+officialHost) {
			continue
		}
		if pathDepth(full) < bestDepth {
			best, bestDepth = full, pathDepth(full)
		}
	}
	if best != "" {
		return best
	}
	return guessIRPath(ctx, client, official)
}

const maxHTMLBytes = 4 << 20

func pathDepth(rawURL string) int {
	path := strings.Split(rawURL, "?")[0]
	path = strings.TrimSuffix(path, "/")
	return len(strings.Split(path, "/")) - 3
}

func guessIRPath(ctx context.Context, client *http.Client, official string) string {
	for _, p := range irGuessPaths {
		candidate := strings.TrimRight(official, "/") + p
		body, _, err := FetchHTTP(ctx, client, candidate, 512<<10)
		if err != nil {
			continue
		}
		if len(body) > 400 {
			return candidate
		}
	}
	return ""
}

func collectDocsFromPage(ctx context.Context, client *http.Client, res *DiscoveryResult) {
	page := res.IRLink
	if page == "" {
		page = res.OfficialWebsite
	}
	scanPDFsOnPage(ctx, client, res, page)
}

// scanAnnualReportPages chases "annual" links one level deep from the official
// site when the first page scan missed annual reports entirely.
func scanAnnualReportPages(ctx context.Context, client *http.Client, base *url.URL, res *DiscoveryResult) {
	body, _, err := FetchHTTP(ctx, client, base.String(), maxHTMLBytes)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	scanned := 0
	for _, a := range anchorsFromHTML(string(body)) {
		low := strings.ToLower(a.href + " " + a.text)
		if !strings.Contains(low, "annual") || strings.Contains(strings.ToLower(a.href), ".pdf") {
			continue
		}
		full := resolveURL(base, a.href)
		if full == "" || seen[strings.ToLower(full)] {
			continue
		}
		seen[strings.ToLower(full)] = true
		if scanned >= 2 {
			return
		}
		scanned++
		log.Printf("[discover] secondary annual-page scan: %s", full)
		scanPDFsOnPage(ctx, client, res, full)
	}
}

func scanPDFsOnPage(ctx context.Context, client *http.Client, res *DiscoveryResult, page string) {
	body, _, err := FetchHTTP(ctx, client, page, maxHTMLBytes)
	if err != nil {
		res.Issues = append(res.Issues, fmt.Sprintf("doc scan of %s failed: %v", page, err))
		return
	}
	base, _ := url.Parse(page)
	for _, a := range anchorsFromHTML(string(body)) {
		if !strings.Contains(strings.ToLower(a.href), ".pdf") {
			continue
		}
		full := resolveURL(base, a.href)
		if full == "" {
			continue
		}
		title := coalesceTitle(a.text, full)
		kind := classifyDoc(title + " " + full)
		if kind == "" {
			continue
		}
		addDoc(res, DocCandidate{URL: full, Title: title, Kind: kind,
			FY: extractFY(title + " " + full), Source: "official_site"})
	}
}

func officialURL(res *DiscoveryResult) *url.URL {
	u, _ := url.Parse(res.OfficialWebsite)
	return u
}

func anchorsFromHTML(body string) []anchor {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var out []anchor
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := ""
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
					break
				}
			}
			out = append(out, anchor{href: href, text: nodeText(n)})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func resolveURL(base *url.URL, href string) string {
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	ref.Fragment = ""
	out := base.ResolveReference(ref)
	if out.Scheme != "http" && out.Scheme != "https" {
		return ""
	}
	s := out.String()
	if strings.HasPrefix(s, "http://") {
		s = strings.Replace(s, "http://", "https://", 1)
	}
	return s
}

func coalesceTitle(anchorText, rawURL string) string {
	if t := strings.TrimSpace(anchorText); t != "" {
		return t
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parts := strings.Split(u.Path, "/")
	return parts[len(parts)-1]
}

func cleanTitle(t string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(t), " ")
}

var docClassifiers = []struct {
	rx   *regexp.Regexp
	kind string
}{
	{regexp.MustCompile(`(?i)(annual|integrated)\s*(report|accounts)|report\s*[-& ]\s*(and\s*)?accounts|annual[-_ ]?report`), docAnnualReport},
	{regexp.MustCompile(`(?i)investor|presentation|analyst|earnings\s*call`), docInvestorPresentation},
}

func classifyDoc(s string) string {
	low := strings.ToLower(s)
	for _, c := range docClassifiers {
		if c.rx.MatchString(low) {
			return c.kind
		}
	}
	return ""
}

var (
	fyRangeRx = regexp.MustCompile(`(?:FY)?\s?(20\d{2})\s*[-–—/]\s*((?:20)?(\d{2}))`)
	fyYearRx  = regexp.MustCompile(`\bFY\s?(20\d{2})\b|\b(20[12]\d)\b`)
)

// extractFY returns canonical fiscal labels like FY2025-26 / FY2025.
func extractFY(s string) string {
	if m := fyRangeRx.FindStringSubmatch(s); m != nil {
		endSuffix := m[3]
		if len(endSuffix) > 2 {
			endSuffix = endSuffix[len(endSuffix)-2:]
		}
		return "FY" + m[1] + "-" + endSuffix
	}
	m := fyYearRx.FindAllStringSubmatch(s, -1)
	year := ""
	for _, mm := range m {
		cand := mm[1]
		if cand == "" {
			cand = mm[2]
		}
		if year == "" || cand > year {
			year = cand
		}
	}
	if year == "" {
		return ""
	}
	return "FY" + year
}

func addDoc(res *DiscoveryResult, d DocCandidate) {
	d.URL = stripQueryJunk(d.URL)
	for i := range res.Docs {
		if res.Docs[i].URL == d.URL {
			if d.Source == "official_site" {
				res.Docs[i].Source = "official_site"
			}
			return
		}
	}
	res.Docs = append(res.Docs, d)
}

func stripQueryJunk(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func countKind(docs []DocCandidate, kind string) int {
	n := 0
	for _, d := range docs {
		if d.Kind == kind {
			n++
		}
	}
	return n
}

func dedupeAndRank(res *DiscoveryResult) {
	seen := make(map[string]bool)
	var kept []DocCandidate
	for _, d := range res.Docs {
		key := strings.ToLower(d.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		d.Latest = latestFYNum(d.FY)
		kept = append(kept, d)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Latest != b.Latest {
			return a.Latest > b.Latest
		}
		return a.Source < b.Source
	})
	res.Docs = kept
}

func latestFYNum(fy string) int {
	all := regexp.MustCompile(`\d+`).FindAllString(fy, -1)
	if len(all) == 0 {
		return 0
	}
	first, _ := strconv.Atoi(all[0])
	last, _ := strconv.Atoi(all[len(all)-1])
	if len(all) > 1 && last < first {
		if last < 100 && first%100+1 == last {
			return first
		}
		return first
	}
	return last
}
