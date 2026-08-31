package tickertape

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// normalize drops empty objects/arrays recursively so incidental differences
// (e.g. an absent "upcoming": []) do not fail the comparison.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			n := normalize(val)
			if isEmpty(n) {
				continue
			}
			out[k] = n
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	default:
		return v
	}
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	default:
		return false
	}
}

func TestExtractMatchesReference(t *testing.T) {
	root := filepath.Join("..", "..", "tickertape_files")

	got, err := Extract("KOTAKBANK", filepath.Join(root, "tickertape_kotak_resp.html"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	want, err := os.ReadFile(filepath.Join(root, "tickertape_kotak_extracted.json"))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}

	var gotV, wantV any
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatalf("extracted JSON invalid: %v", err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatalf("reference JSON invalid: %v", err)
	}

	gotN := normalize(gotV).(map[string]any)
	wantN := normalize(wantV).(map[string]any)

	for _, key := range extractedKeys {
		g, ok := gotN[key]
		if !ok {
			t.Errorf("missing section %q", key)
			continue
		}
		w, ok := wantN[key]
		if !ok {
			t.Errorf("reference has no section %q", key)
			continue
		}
		if gj, wj := marshal(g), marshal(w); string(gj) != string(wj) {
			t.Errorf("section %q differs:\n got: %.200s\nwant: %.200s", key, gj, wj)
		}
	}
}

func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("marshal-error")
	}
	return b
}
