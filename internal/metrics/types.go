// Package metrics derives key fundamental metrics from the saved tickertape
// financial statements (income / balance sheet / cash flow) and stores one
// document per (symbol, reporting) in the key_metrics collection.
//
// Conventions:
//   - all ratio-type metrics (margins, growth, returns, coverage) are decimal
//     fractions: 0.153 means 15.3%
//   - day-count metrics (DSO/DIO/DPO, working-capital cycle) are in days
//   - money values keep the source unit (tickertape: crores)
//   - a metric is null whenever its inputs are missing or the formula is
//     undefined (e.g. CAGR over a non-positive base)
//
// Not computable from the current sources (Alpha Vantage quotes + tickertape
// financials): volume vs price-led revenue split and guidance vs actual
// growth - both need company disclosures / management commentary.
//
// Reliability notes:
//   - interest coverage uses tickertape "Interest & Other Items" (incIoi),
//     which may include non-interest items, so treat it as approximate
//   - ROIC = NOPAT / average invested capital (debt + equity - cash & ST
//     investments) using the effective tax rate; only set when all inputs
//     exist (typically non-financial companies)
//   - working-capital day counts use total revenue as a COGS proxy when
//     gross profit is not reported (e.g. banks)
package metrics

import (
	"time"
)

// Statements are the tickertape statement rows, aligned per fiscal year end
// (normalised "2006-01-02"). TTM rows have no endDate and are dropped.
type Statements struct {
	Symbol    string
	Reporting string   // reporting basis used: "consolidated" or "standalone"
	Years     []string // fiscal year end dates, ascending
	Income    map[string]IncomeRow
	Balance   map[string]BalanceRow
	Cashflow  map[string]CashflowRow
}

// IncomeRow is one annual income statement row (tickertape income-*) year.
type IncomeRow struct {
	DisplayPeriod string   `bson:"displayPeriod" json:"displayPeriod"`
	EndDate       string   `bson:"endDate" json:"endDate"`
	Reporting     string   `bson:"reporting" json:"reporting"`
	TotalRevenue  *float64 `bson:"incTrev" json:"incTrev"`
	GrossProfit   *float64 `bson:"incGpro" json:"incGpro"`
	EBITDA        *float64 `bson:"incEbi" json:"incEbi"`
	Depreciation  *float64 `bson:"incDep" json:"incDep"`
	PBIT          *float64 `bson:"incPbi" json:"incPbi"`
	InterestOther *float64 `bson:"incIoi" json:"incIoi"`
	PBT           *float64 `bson:"incPbt" json:"incPbt"`
	TaxOther      *float64 `bson:"incToi" json:"incToi"`
	NetIncome     *float64 `bson:"incNinc" json:"incNinc"`
	EPS           *float64 `bson:"incEps" json:"incEps"`
}

// BalanceRow is one annual balance sheet row (tickertape balancesheet-*).
type BalanceRow struct {
	DisplayPeriod     string   `bson:"displayPeriod" json:"displayPeriod"`
	EndDate           string   `bson:"endDate" json:"endDate"`
	Reporting         string   `bson:"reporting" json:"reporting"`
	CashAndSTI        *float64 `bson:"balCsti" json:"balCsti"`
	Receivables       *float64 `bson:"balTrec" json:"balTrec"`
	Inventory         *float64 `bson:"balTinv" json:"balTinv"`
	Payables          *float64 `bson:"balAccp" json:"balAccp"`
	CurrentAssets     *float64 `bson:"balTca" json:"balTca"`
	CurrentLiab       *float64 `bson:"balTcl" json:"balTcl"`
	TotalDebt         *float64 `bson:"balTdeb" json:"balTdeb"`
	TotalAssets       *float64 `bson:"balTota" json:"balTota"`
	MinorityInterest  *float64 `bson:"balMint" json:"balMint"`
	TotalEquity       *float64 `bson:"balTeq" json:"balTeq"`
	SharesOutstanding *float64 `bson:"balTcso" json:"balTcso"`
}

// CashflowRow is one annual cash flow row (tickertape cashflow-*).
type CashflowRow struct {
	DisplayPeriod string   `bson:"displayPeriod" json:"displayPeriod"`
	EndDate       string   `bson:"endDate" json:"endDate"`
	Reporting     string   `bson:"reporting" json:"reporting"`
	CFO           *float64 `bson:"cafCfoa" json:"cafCfoa"`
	Capex         *float64 `bson:"cafCexp" json:"cafCexp"`
	FCF           *float64 `bson:"cafFcf" json:"cafFcf"`
}

// KeyMetrics is the document stored per (symbol, reporting) in key_metrics.
type KeyMetrics struct {
	Symbol      string        `bson:"symbol" json:"symbol"`
	Reporting   string        `bson:"reporting" json:"reporting"`
	SourceDay   string        `bson:"source_day" json:"source_day"`
	ComputedAt  time.Time     `bson:"computed_at" json:"computed_at"`
	CreatedAt   time.Time     `bson:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt   time.Time     `bson:"updated_at" json:"updated_at"`
	FirstFY     string        `bson:"first_fy" json:"first_fy"`
	LastFY      string        `bson:"last_fy" json:"last_fy"`
	YearsCount  int           `bson:"years_count" json:"years_count"`
	Years       []YearMetrics `bson:"years" json:"years"`
	Summary     Summary       `bson:"summary" json:"summary"`
	Notes       []string      `bson:"notes" json:"notes"`
	SourceNotes []string      `bson:"source_notes,omitempty" json:"source_notes,omitempty"`
}

// YearMetrics holds every metric computed for one fiscal year.
type YearMetrics struct {
	FY       string          `bson:"fy" json:"fy"`
	EndDate  string          `bson:"end_date" json:"end_date"`
	Inputs   Inputs          `bson:"inputs" json:"inputs"`
	Growth   GrowthMetrics   `bson:"growth" json:"growth"`
	Margins  MarginMetrics   `bson:"margins" json:"margins"`
	Returns  ReturnMetrics   `bson:"returns" json:"returns"`
	Health   HealthMetrics   `bson:"health" json:"health"`
	Cashflow CashflowMetrics `bson:"cashflow" json:"cashflow"`
	DataGaps []string        `bson:"data_gaps,omitempty" json:"data_gaps,omitempty"`
}

// Inputs keeps the source values each year's metrics are derived from, so
// downstream consumers never have to re-join the raw statements.
type Inputs struct {
	TotalRevenue   *float64 `bson:"total_revenue" json:"total_revenue"`
	GrossProfit    *float64 `bson:"gross_profit" json:"gross_profit"`
	EBITDA         *float64 `bson:"ebitda" json:"ebitda"`
	EBIT           *float64 `bson:"ebit" json:"ebit"`
	PBT            *float64 `bson:"pbt" json:"pbt"`
	NetIncome      *float64 `bson:"net_income" json:"net_income"`
	EPS            *float64 `bson:"eps" json:"eps"`
	CFO            *float64 `bson:"cfo" json:"cfo"`
	Capex          *float64 `bson:"capex" json:"capex"`
	FCF            *float64 `bson:"fcf" json:"fcf"`
	TotalEquity    *float64 `bson:"total_equity" json:"total_equity"`
	MinorityInt    *float64 `bson:"minority_interest" json:"minority_interest"`
	NetWorth       *float64 `bson:"net_worth" json:"net_worth"`
	TotalDebt      *float64 `bson:"total_debt" json:"total_debt"`
	CashAndSTI     *float64 `bson:"cash_and_sti" json:"cash_and_sti"`
	TotalAssets    *float64 `bson:"total_assets" json:"total_assets"`
	CurrentAssets  *float64 `bson:"current_assets" json:"current_assets"`
	CurrentLiab    *float64 `bson:"current_liabilities" json:"current_liabilities"`
	Receivables    *float64 `bson:"receivables" json:"receivables"`
	Inventory      *float64 `bson:"inventory" json:"inventory"`
	Payables       *float64 `bson:"payables" json:"payables"`
	SharesOutstand *float64 `bson:"shares_outstanding" json:"shares_outstanding"`
}

// GrowthMetrics covers topline/bottomline growth, CAGRs and operating leverage.
type GrowthMetrics struct {
	RevenueYoY    *float64 `bson:"revenue_yoy" json:"revenue_yoy"`
	RevenueCAGR3Y *float64 `bson:"revenue_cagr_3y" json:"revenue_cagr_3y"`
	RevenueCAGR5Y *float64 `bson:"revenue_cagr_5y" json:"revenue_cagr_5y"`
	EBITDAYoY     *float64 `bson:"ebitda_yoy" json:"ebitda_yoy"`
	EBITDACAGR3Y  *float64 `bson:"ebitda_cagr_3y" json:"ebitda_cagr_3y"`
	EBITDACAGR5Y  *float64 `bson:"ebitda_cagr_5y" json:"ebitda_cagr_5y"`
	EBITYoY       *float64 `bson:"ebit_yoy" json:"ebit_yoy"`
	EBITCAGR3Y    *float64 `bson:"ebit_cagr_3y" json:"ebit_cagr_3y"`
	EBITCAGR5Y    *float64 `bson:"ebit_cagr_5y" json:"ebit_cagr_5y"`
	PATYoY        *float64 `bson:"pat_yoy" json:"pat_yoy"`
	PATCAGR3Y     *float64 `bson:"pat_cagr_3y" json:"pat_cagr_3y"`
	PATCAGR5Y     *float64 `bson:"pat_cagr_5y" json:"pat_cagr_5y"`
	EPSYoY        *float64 `bson:"eps_yoy" json:"eps_yoy"`
	EPSCAGR3Y     *float64 `bson:"eps_cagr_3y" json:"eps_cagr_3y"`
	EPSCAGR5Y     *float64 `bson:"eps_cagr_5y" json:"eps_cagr_5y"`
	DOL           *float64 `bson:"dol_ebit" json:"dol_ebit"`
	DOLEBITDA     *float64 `bson:"dol_ebitda" json:"dol_ebitda"`
}

// MarginMetrics are the year's profitability margins.
type MarginMetrics struct {
	Gross  *float64 `bson:"gross" json:"gross"`
	EBITDA *float64 `bson:"ebitda" json:"ebitda"`
	EBIT   *float64 `bson:"ebit" json:"ebit"`
	Net    *float64 `bson:"net" json:"net"`
}

// ReturnMetrics are the year's returns on capital.
type ReturnMetrics struct {
	ROE  *float64 `bson:"roe" json:"roe"`
	RONW *float64 `bson:"ronw" json:"ronw"`
	ROCE *float64 `bson:"roce" json:"roce"`
	ROIC *float64 `bson:"roic" json:"roic"`
}

// HealthMetrics are the year's balance-sheet strength and liquidity metrics.
type HealthMetrics struct {
	DebtToEquity      *float64 `bson:"debt_to_equity" json:"debt_to_equity"`
	NetDebt           *float64 `bson:"net_debt" json:"net_debt"`
	NetDebtToEBITDA   *float64 `bson:"net_debt_to_ebitda" json:"net_debt_to_ebitda"`
	InterestCoverage  *float64 `bson:"interest_coverage" json:"interest_coverage"`
	CurrentRatio      *float64 `bson:"current_ratio" json:"current_ratio"`
	DSO               *float64 `bson:"dso_days" json:"dso_days"`
	DIO               *float64 `bson:"dio_days" json:"dio_days"`
	DPO               *float64 `bson:"dpo_days" json:"dpo_days"`
	WCCycleDays       *float64 `bson:"wc_cycle_days" json:"wc_cycle_days"`
	ReceivablesYoY    *float64 `bson:"receivables_yoy" json:"receivables_yoy"`
	ReceivablesPctRev *float64 `bson:"receivables_pct_revenue" json:"receivables_pct_revenue"`
	InventoryYoY      *float64 `bson:"inventory_yoy" json:"inventory_yoy"`
	InventoryPctRev   *float64 `bson:"inventory_pct_revenue" json:"inventory_pct_revenue"`
	PayablesYoY       *float64 `bson:"payables_yoy" json:"payables_yoy"`
	PayablesPctCOGS   *float64 `bson:"payables_pct_cogs" json:"payables_pct_cogs"`
}

// CashflowMetrics are the year's cash generation and conversion metrics.
type CashflowMetrics struct {
	CFO         *float64 `bson:"cfo" json:"cfo"`
	Capex       *float64 `bson:"capex" json:"capex"`
	FCF         *float64 `bson:"fcf" json:"fcf"`
	FCFMargin   *float64 `bson:"fcf_margin" json:"fcf_margin"`
	CFOToPAT    *float64 `bson:"cfo_to_pat" json:"cfo_to_pat"`
	FCFToPAT    *float64 `bson:"fcf_to_pat" json:"fcf_to_pat"`
	CFOToEBITDA *float64 `bson:"cfo_to_ebitda" json:"cfo_to_ebitda"`
}

// MarginStats summarise one margin across the whole history: level, trend
// (OLS slope per year) and stability (population std dev).
type MarginStats struct {
	Mean         *float64 `bson:"mean" json:"mean"`
	StdDev       *float64 `bson:"stddev" json:"stddev"`
	TrendPerYear *float64 `bson:"trend_per_year" json:"trend_per_year"`
	Min          *float64 `bson:"min" json:"min"`
	Max          *float64 `bson:"max" json:"max"`
	Direction    string   `bson:"direction,omitempty" json:"direction,omitempty"`
}

// MarginStability is the trend + stability block for the four margins.
type MarginStability struct {
	Gross  MarginStats `bson:"gross" json:"gross"`
	EBITDA MarginStats `bson:"ebitda" json:"ebitda"`
	EBIT   MarginStats `bson:"ebit" json:"ebit"`
	Net    MarginStats `bson:"net" json:"net"`
}

// CAGRSummary holds trailing and full-window CAGRs measured from the latest
// fiscal year.
type CAGRSummary struct {
	Revenue3Y   *float64 `bson:"revenue_3y" json:"revenue_3y"`
	Revenue5Y   *float64 `bson:"revenue_5y" json:"revenue_5y"`
	RevenueFull *float64 `bson:"revenue_full" json:"revenue_full"`
	EBITDA3Y    *float64 `bson:"ebitda_3y" json:"ebitda_3y"`
	EBITDA5Y    *float64 `bson:"ebitda_5y" json:"ebitda_5y"`
	EBITDAFull  *float64 `bson:"ebitda_full" json:"ebitda_full"`
	EBIT3Y      *float64 `bson:"ebit_3y" json:"ebit_3y"`
	EBIT5Y      *float64 `bson:"ebit_5y" json:"ebit_5y"`
	EBITFull    *float64 `bson:"ebit_full" json:"ebit_full"`
	PAT3Y       *float64 `bson:"pat_3y" json:"pat_3y"`
	PAT5Y       *float64 `bson:"pat_5y" json:"pat_5y"`
	PATFull     *float64 `bson:"pat_full" json:"pat_full"`
	EPS3Y       *float64 `bson:"eps_3y" json:"eps_3y"`
	EPS5Y       *float64 `bson:"eps_5y" json:"eps_5y"`
	EPSFull     *float64 `bson:"eps_full" json:"eps_full"`
}

// ReturnSummary is the latest and history-mean return metrics.
type ReturnSummary struct {
	ROELatest  *float64 `bson:"roe_latest" json:"roe_latest"`
	ROEMean    *float64 `bson:"roe_mean" json:"roe_mean"`
	RONWLatest *float64 `bson:"ronw_latest" json:"ronw_latest"`
	RONWMean   *float64 `bson:"ronw_mean" json:"ronw_mean"`
	ROCELatest *float64 `bson:"roce_latest" json:"roce_latest"`
	ROCEMean   *float64 `bson:"roce_mean" json:"roce_mean"`
	ROICLatest *float64 `bson:"roic_latest" json:"roic_latest"`
	ROICMean   *float64 `bson:"roic_mean" json:"roic_mean"`
}

// HealthSummary is the latest balance-sheet health snapshot.
type HealthSummary struct {
	DebtToEquity     *float64 `bson:"debt_to_equity" json:"debt_to_equity"`
	NetDebtToEBITDA  *float64 `bson:"net_debt_to_ebitda" json:"net_debt_to_ebitda"`
	InterestCoverage *float64 `bson:"interest_coverage" json:"interest_coverage"`
	CurrentRatio     *float64 `bson:"current_ratio" json:"current_ratio"`
	WCCycleDays      *float64 `bson:"wc_cycle_days" json:"wc_cycle_days"`
}

// CashflowSummary is the latest cash generation snapshot plus history means.
type CashflowSummary struct {
	FCFLatest       *float64 `bson:"fcf_latest" json:"fcf_latest"`
	FCFMarginLatest *float64 `bson:"fcf_margin_latest" json:"fcf_margin_latest"`
	FCFMarginMean   *float64 `bson:"fcf_margin_mean" json:"fcf_margin_mean"`
	CFOToPATLatest  *float64 `bson:"cfo_to_pat_latest" json:"cfo_to_pat_latest"`
	CFOToPATMean    *float64 `bson:"cfo_to_pat_mean" json:"cfo_to_pat_mean"`
}

// Summary is the cross-year view stored alongside the per-year metrics.
type Summary struct {
	CAGR     CAGRSummary     `bson:"cagr" json:"cagr"`
	Margins  MarginStability `bson:"margins" json:"margins"`
	Returns  ReturnSummary   `bson:"returns" json:"returns"`
	Health   HealthSummary   `bson:"health" json:"health"`
	Cashflow CashflowSummary `bson:"cashflow" json:"cashflow"`
}
