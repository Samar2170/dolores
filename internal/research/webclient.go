package research

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"

// NewBrowserClient returns an HTTP client sized for document downloads and
// tolerant of slow Indian corporate CDNs.
func NewBrowserClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// FetchHTTP GETs url with browser-like headers (several Indian corp sites run
// header-based WAFs), enforces maxBytes, and returns body bytes plus the
// final response Content-Type.
func FetchHTTP(ctx context.Context, client *http.Client, url string, maxBytes int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("%s %s", resp.Status, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("read %s: %w", url, err)
	}
	if int64(len(body)) >= maxBytes {
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("body of %s exceeds cap of %d bytes", url, maxBytes)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// IsPDF reports whether data begins with the PDF magic bytes.
func IsPDF(data []byte) bool {
	return len(data) > 4 && string(data[:4]) == "%PDF"
}
