package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dolores/internal/llm"
)

// promptPath holds the financial-analysis agent's system prompt, relative to
// the project root the commands run from.
const promptPath = "prompts/financial_analysis.md"

// Run executes the financial-analysis agent: the instructions in
// prompts/financial_analysis.md are its system prompt and the collected data
// is handed over as the task input. It returns the analyst's report text.
func Run(ctx context.Context, lg *llm.Client, data *Data) (string, error) {
	system, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("analysis: read %s: %w", promptPath, err)
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("analysis: encode collected data: %w", err)
	}

	user := fmt.Sprintf("Perform the financial analysis for %s. The collected data for the company follows.\n\n<collected_data>\n%s\n</collected_data>",
		data.Symbol, payload)
	log.Printf("[analysis] %s: running financial-analysis agent (%d bytes prompt)", data.Symbol, len(user))

	report, err := lg.CompleteText(ctx, string(system), user)
	if err != nil {
		return "", fmt.Errorf("analysis: financial-analysis agent: %w", err)
	}
	return report, nil
}