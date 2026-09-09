package agent

var promptDict = map[string]string{
	"financial":  "prompts/financial_analysis.md",
	"business":   "prompts/business_competitive_analysis.md",
	"management": "prompts/management_analysis.md",
}

// var dataDict = map[string]analysis.AnalysisData{
// 	"financial": &analysis.FinancialStatement{},
// }

func GetPromptFile(promptType string) (string, bool) {
	if file, ok := promptDict[promptType]; ok {
		return file, true
	}
	return "", false
}
