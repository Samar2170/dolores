package tickertape

var statementKeyMap = map[string]map[string]string{
	"inc": incomeStatementKeys,
	"bal": balanceSheetKeys,
	"caf": cashFlowKeys,
}

func SwapKeys(input map[string]interface{}) map[string]interface{} {
	output := make(map[string]interface{})
	for k, v := range input {
		if len(k) >= 3 {
			if keyMap, ok := statementKeyMap[k[:3]]; ok {
				if newKey, ok := keyMap[k]; ok {
					output[newKey] = v
					continue
				}
			}
		}
		output[k] = v
	}
	return output
}

var incomeStatementKeys = map[string]string{
	"incTrev": "Total Revenue",
	"incOpe":  "Operating Income",
	"incEbi":  "Earnings Before Interest and Taxes",
	"incDep":  "Depreciation",
	"incPbi":  "Profit Before Interest",
	"incIoi":  "Interest and Other Items",
	"incPbt":  "Profit Before Tax",
	"incToi":  "Taxes and Other Items",
	"incNinc": "Net Income",
	"incEps":  "Earnings Per Share",
	"incDps":  "Dividends Per Share",
	"incPyr":  "Price to Yield Ratio",
	"incEpc":  "Employee Cost",

	"incCrev": "Cost of Revenue",
	"incGpro": "Gross Profit",
	"incOpc":  "Operating Costs",
	"incRaw":  "Raw Materials",
	"incPfc":  "Power and Fuel Cost",
	"incSga":  "Selling, General and Administrative Expenses",
}

var balanceSheetKeys = map[string]string{
	"balCsti": "Cash and Short Term Investments",
	"balTrec": "Total Receivables",
	"balTinv": "Total Inventory",
	"balOca":  "Other Current Assets",
	"balTca":  "Total Current Assets",
	"balNetl": "Net Liabilities",
	"balNppe": "Net Property, Plant and Equipment",
	"balGint": "Goodwill",
	"balLti":  "Long Term Investments",
	"balOtha": "Other Total Assets",
	"balTota": "Total Assets",
	"balAccp": "Accounts Payable",
	"balTdep": "Total Deposits",
	"balOcl":  "Other Current Liabilities",
	"balTcl":  "Total Current Liabilities",
	"balTltd": "Total Long Term Debt",
	"balTdeb": "Total Debt",
	"balDit":  "Deferred Tax Liabilities",
	"balMint": "Minority Interest",
	"balOthl": "Other Total Liabilities",
	"balTotl": "Total Liabilities",
	"balComs": "Common Stock",
	"balApic": "Additional Paid-in Capital",
	"balRtne": "Reserves and Retained Earnings",
	"balOeq":  "Other Equity",
	"balTeq":  "Total Equity",
	"balTlse": "Total Shareholders' Equity",
	"balTcso": "Total Common Stock",
	"balTpso": "Total Preferred Stock",
	"balNca":  "Net Current Assets",
	"balCa":   "Current Assets",
	"balNcl":  "Net Current Liabilities",
	"balDta":  "Deferred Tax Assets",
}

var cashFlowKeys = map[string]string{
	"cafCiwc": "Change in Working Capital",
	"cafCfoa": "Cash Flow from Operating Activities",
	"cafCexp": "Capital Expenditures",
	"cafCfia": "Cash Flow from Investing Activities",
	"cafTcdp": "Total Cash Dividends Paid",
	"cafCffa": "Cash Flow from Financing Activities",
	"cafNcic": "Net Changes in Cash",
	"cafFcf":  "Free Cash Flow",
}
