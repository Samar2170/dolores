package models

// Market data collections: one per fetcher output kind.
const (
	ColAVTimeSeries  = "av_timeseries"
	ColAVGlobalQuote = "av_global_quote"
	ColIndiasmStock  = "indiasm_stock"
)

// Derived data collections.
const (
	ColKeyMetrics = "key_metrics"
)

// Tickertape collections: one per extracted pageProps section.
const (
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
