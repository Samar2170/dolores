package llm

import (
	"strings"
	"unicode/utf8"
)

// EstimateTokens returns a rough token count for s, approximating the
// BPE tokenizers used by modern LLMs: a token averages ~4 characters
// and English text averages ~1.3 tokens per word. Good enough for
// budgeting; do not use it for billing.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	words := strings.Fields(s)
	chars := utf8.RuneCountInString(s)
	return max(len(words), (chars+3)/4)
}
