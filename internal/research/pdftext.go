package research

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// PageText holds the extracted plain text of one PDF page.
type PageText struct {
	Page int // 0 = whole document (poppler fallback path)
	Text string
}

// ExtractPDFPages pulls plain text out of every readable page. Image-only or
// exotic-font pages are skipped. Returns page texts, total page count, and
// per-page issues encountered. When the result is clearly unusable but the
// pdftotext binary is available, it falls back to poppler for a
// whole-document pass.
func ExtractPDFPages(data []byte) ([]PageText, int, []string, error) {
	br := bytes.NewReader(data)
	reader, err := pdf.NewReader(br, int64(len(data)))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("open pdf: %w", err)
	}

	total := reader.NumPage()
	var out []PageText
	var issues []string
	for i := 1; i <= total; i++ {
		txt, err := pageText(reader, i)
		if err != nil {
			issues = append(issues, fmt.Sprintf("page %d: %v", i, err))
			continue
		}
		if strings.TrimSpace(txt) == "" {
			continue
		}
		out = append(out, PageText{Page: i, Text: txt})
	}

	totalChars := 0
	for _, p := range out {
		totalChars += len(p.Text)
	}
	if total >= 10 && totalChars < 100 && lookPath("pdftotext") {
		if txt, err := pdftotextExtract(data); err == nil && strings.TrimSpace(txt) != "" {
			return []PageText{{Page: 0, Text: txt}}, total, append(issues, "fallback: pdftotext"), nil
		}
	}
	return out, total, issues, nil
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func pdftotextExtract(data []byte) (string, error) {
	tmp, err := os.CreateTemp("", "research-*.pdf")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	cmd := exec.Command("pdftotext", "-layout", tmp.Name(), "-")
	var out strings.Builder
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %v: %s", err, stderr.String())
	}
	return out.String(), nil
}

func pageText(reader *pdf.Reader, n int) (txt string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("page %d panicked: %v", n, r)
			txt = ""
		}
	}()
	page := reader.Page(n)
	if page.V.IsNull() {
		return "", fmt.Errorf("page %d unreadable", n)
	}
	return page.GetPlainText(nil)
}

// SelectPages scores pages against keywords and returns up to maxChars worth
// of the most relevant page texts joined with [page N] markers.
func SelectPages(pages []PageText, keywords []string, maxChars int) string {
	type scored struct {
		idx   int
		score int
	}
	var hits []scored
	for i, p := range pages {
		lower := strings.ToLower(p.Text)
		s := 0
		for _, kw := range keywords {
			s += strings.Count(lower, kw)
		}
		if s > 0 {
			hits = append(hits, scored{i, s})
		}
	}
	byScore := func(a, b scored) bool { return a.score > b.score }
	for a := 1; a < len(hits); a++ {
		for b := a; b > 0 && byScore(hits[b], hits[b-1]); b-- {
			hits[b], hits[b-1] = hits[b-1], hits[b]
		}
	}

	var parts []string
	used := 0
	for _, h := range hits {
		if used >= maxChars {
			break
		}
		t := pages[h.idx].Text
		if used+len(t) > maxChars {
			t = t[:maxChars-used]
		}
		parts = append(parts, fmt.Sprintf("[page %d]\n%s", pages[h.idx].Page, t))
		used += len(t)
	}
	return strings.Join(parts, "\n\n")
}

// joinPageTexts renders page texts in the same "[page N]" format SelectPages
// emits, so the parsed-text archive file can be re-split losslessly.
func joinPageTexts(pages []PageText) string {
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		parts = append(parts, fmt.Sprintf("[page %d]\n%s", p.Page, p.Text))
	}
	return strings.Join(parts, "\n\n")
}

var pageMarkerRx = regexp.MustCompile(`(?m)^\[page (\d+)\]\n`)

// splitPageTexts reverses joinPageTexts for parsed-text files pulled back
// from the archive.
func splitPageTexts(content string) []PageText {
	locs := pageMarkerRx.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([]PageText, 0, len(locs))
	for i, loc := range locs {
		page, err := strconv.Atoi(content[loc[2]:loc[3]])
		if err != nil {
			continue
		}
		start := loc[1]
		end := len(content)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, PageText{Page: page, Text: strings.TrimSpace(content[start:end])})
	}
	return out
}
