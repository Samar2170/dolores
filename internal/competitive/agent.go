package competitive

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dolores/internal/llm"
)

// promptPath holds the business & competitive research agent's system prompt,
// relative to the project root the commands run from.
const promptPath = "prompts/business_competitive_analysis.md"

// Run executes the business & competitive research agent: the instructions in
// prompts/business_competitive_analysis.md are its system prompt and the
// parsed documents loaded from Archivus are handed over as the task input.
// It returns the analyst's report text.
func Run(ctx context.Context, lg *llm.Client, data *Data) (string, error) {
	system, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("competitive: read %s: %w", promptPath, err)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("competitive: encode collected documents: %w", err)
	}

	user := fmt.Sprintf("Perform the business & competitive research for %s. The parsed source documents for the company follow.\n\n<parsed_documents>\n%s\n</parsed_documents>",
		data.Symbol, payload)
	log.Printf("[competitive] %s: running business & competitive research agent (%d bytes prompt)", data.Symbol, len(user))

	report, err := lg.CompleteText(ctx, string(system), user)
	if err != nil {
		return "", fmt.Errorf("competitive: business & competitive research agent: %w", err)
	}
	return report, nil
}
