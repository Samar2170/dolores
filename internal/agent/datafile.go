package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"dolores/internal/research"
)

// LoadDataFile turns a local data file into prompt-ready text: PDFs are parsed
// (keyword-ranked when keywords are given), text files are passed through,
// other binaries are rejected.
func LoadDataFile(path string, keywords []string, maxChars int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("data file %s is empty", path)
	}
	if research.IsPDF(data) {
		pages, total, _, err := research.ExtractPDFPages(data)
		if err != nil {
			return "", fmt.Errorf("parse pdf %s: %w", path, err)
		}
		if len(pages) == 0 {
			return "", fmt.Errorf("pdf %s has no text layer (likely scanned)", path)
		}
		if maxChars <= 0 {
			maxChars = 60000
		}
		return fmt.Sprintf("<<file %s: pdf, %d/%d pages with text>>\n%s", filepath.Base(path), len(pages), total, pagesToText(pages, keywords, maxChars)), nil
	}
	if bytes.ContainsRune(data, 0) {
		return "", fmt.Errorf("data file %s is binary and not a pdf", path)
	}
	if maxChars <= 0 {
		maxChars = 120000
	}
	return fmt.Sprintf("<<file %s>>\n%s", filepath.Base(path), clipText(string(data), maxChars)), nil
}
