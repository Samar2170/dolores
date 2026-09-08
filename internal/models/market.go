package models

// Market data collections: one per fetcher output kind.
const (
	ColAVTimeSeries  = "av_timeseries"
	ColAVGlobalQuote = "av_global_quote"
	ColIndiasmStock  = "indiasm_stock"

	// Derived data collections.
	ColKeyMetrics = "key_metrics"

	// Financial-analysis outputs.
	ColCompanyFinancialAnalysis = "company_financial_analysis"

	// Business & competitive-analysis outputs.
	ColCompanyCompetitiveAnalysis = "company_business_and_competitive_analysis"

	// Management, governance & risk-analysis outputs.
	ColCompanyManagementAnalysis = "company_management_analysis"

	// Tickertape collections: one per extracted pageProps section.
	ColTickerCommentary          = "tickertape_commentary"
	ColTickerFaq                 = "tickertape_faq"
	ColTickerIncomeAnnual        = "tickertape_income_annual"
	ColTickerIncomeInterim       = "tickertape_income_interim"
	ColTickerBalancesheetAnnual  = "tickertape_balancesheet_annual"
	ColTickerCashflowAnnual      = "tickertape_cashflow_annual"
	ColTickerPeers               = "tickertape_peers_technical"
	ColTickerEventsCorpActions   = "tickertape_events_corp_actions"
	ColTickerEventsAnnouncements = "tickertape_events_announcements"
	ColTickerEventsLegal         = "tickertape_events_legal"
)
