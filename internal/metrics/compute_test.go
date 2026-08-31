package metrics

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func ptr(v float64) *float64 { return &v }

func val(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatalf("expected non-nil metric")
	}
	return *p
}

func near(t *testing.T, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("got %v, want %v (±%v)", got, want, tol)
	}
}

// fixtureStatements builds statements from the checked-in Kotak extract.
func fixtureStatements(t *testing.T) *Statements {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tickertape_files", "tickertape_kotak_extracted.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	var income []IncomeRow
	if err := json.Unmarshal(sections["income-normal-annual"], &income); err != nil {
		t.Fatalf("decode income: %v", err)
	}
	var balance []BalanceRow
	if err := json.Unmarshal(sections["balancesheet-normal-annual"], &balance); err != nil {
		t.Fatalf("decode balance: %v", err)
	}
	var cashflow []CashflowRow
	if err := json.Unmarshal(sections["cashflow-normal-annual"], &cashflow); err != nil {
		t.Fatalf("decode cashflow: %v", err)
	}
	return BuildStatements("KOTAKBANK", income, balance, cashflow)
}

func TestBuildStatementsFixture(t *testing.T) {
	st := fixtureStatements(t)

	if st.Reporting != "consolidated" {
		t.Errorf("reporting = %q, want consolidated", st.Reporting)
	}
	if len(st.Years) != 10 {
		t.Fatalf("years = %d, want 10 (FY2017..FY2026, TTM dropped)", len(st.Years))
	}
	if st.Years[0] != "2017-03-31" || st.Years[len(st.Years)-1] != "2026-03-31" {
		t.Errorf("year bounds wrong: %s..%s", st.Years[0], st.Years[len(st.Years)-1])
	}
	if _, ok := st.Income["2017-03-31"]; !ok {
		t.Errorf("income FY2017 not aligned")
	}
	if _, ok := st.Cashflow["2017-03-31"]; !ok {
		t.Errorf("cashflow FY2017 not aligned")
	}
}

func TestComputeFixture(t *testing.T) {
	doc := Compute(fixtureStatements(t))

	if doc.Reporting != "consolidated" || doc.FirstFY != "FY 2017" || doc.LastFY != "FY 2026" || doc.YearsCount != 10 {
		t.Fatalf("doc header wrong: %+v", doc)
	}

	// --- FY2018 (index 1) ---
	fy18 := doc.Years[1]
	if fy18.FY != "FY 2018" {
		t.Fatalf("years[1].FY = %q", fy18.FY)
	}
	// Revenue YoY: 38813.31 / 33983.77 - 1
	near(t, val(t, fy18.Growth.RevenueYoY), 38813.31/33983.77-1, 1e-6)
	// PAT YoY: 6200.98 / 4940.44 - 1
	near(t, val(t, fy18.Growth.PATYoY), 6200.979999999992/4940.439999999993-1, 1e-6)
	// Net margin: 6200.98 / 38813.31
	near(t, val(t, fy18.Margins.Net), 6200.979999999992/38813.31, 1e-6)
	// Gross margin absent (bank): nil
	if fy18.Margins.Gross != nil {
		t.Errorf("gross margin should be nil for Kotak, got %v", *fy18.Margins.Gross)
	}
	// EBITDA margin: 9541.66 / 38813.31
	near(t, val(t, fy18.Margins.EBITDA), 9541.659999999993/38813.31, 1e-6)
	// DOL: %dEBIT / %drev (intermediates are rounded to 6dp).
	ebitG := 9158.229999999992/7331.939999999994 - 1
	revG := 38813.31/33983.77 - 1
	near(t, val(t, fy18.Growth.DOL), ebitG/revG, 1e-4)
	// ROE FY2018: PAT / avg(equity 50488.23, 38967.14)
	near(t, val(t, fy18.Returns.ROE), 6200.979999999992/((50488.229999999996+38967.14)/2), 1e-6)
	// RONW FY2018: net worth FY2018 = 50488.23-0, FY2017 = 38967.14-474.43
	nw17, nw18 := 38967.14-474.43, 50488.229999999996-0
	near(t, val(t, fy18.Returns.RONW), 6200.979999999992/((nw17+nw18)/2), 1e-6)
	// ROCE: PBIT / avg(CE) where CE = assets - current liabilities
	ce17, ce18 := 276187.55-155540, 337720.4700000001-191235.8
	near(t, val(t, fy18.Returns.ROCE), 9158.229999999992/((ce17+ce18)/2), 1e-6)
	// Debt/equity + net debt/EBITDA absent for a bank (no total debt).
	if fy18.Health.DebtToEquity != nil || fy18.Health.NetDebtToEBITDA != nil {
		t.Errorf("bank debt metrics should be nil")
	}
	// Current ratio: 24400.63 / 191235.8
	near(t, val(t, fy18.Health.CurrentRatio), 24400.629999999997/191235.8, 1e-6)
	// FCF straight from tickertape.
	near(t, val(t, fy18.Cashflow.FCF), -10818.25, 1e-6)
	// CFO/PAT: -10392.41 / 6200.98
	near(t, val(t, fy18.Cashflow.CFOToPAT), -10392.41/6200.979999999992, 1e-6)

	// Interest coverage nil: incIoi not reported for banks.
	if fy18.Health.InterestCoverage != nil {
		t.Errorf("interest coverage should be nil, got %v", *fy18.Health.InterestCoverage)
	}

	// --- FY2026 (index 9): 5y CAGRs present, 3y CAGRs present ---
	fy26 := doc.Years[9]
	// Revenue 5y CAGR: (111168.26 / 56407.51)^(1/5) - 1  [FY2021 base]
	want := math.Pow(111168.26000000001/56407.51, 1.0/5) - 1
	near(t, val(t, fy26.Growth.RevenueCAGR5Y), want, 1e-6)
	if fy26.Growth.RevenueCAGR3Y == nil {
		t.Errorf("FY2026 revenue 3y CAGR missing")
	}
	// Summary CAGRs mirror the latest year.
	near(t, val(t, doc.Summary.CAGR.Revenue5Y), want, 1e-6)
	if doc.Summary.CAGR.RevenueFull == nil {
		t.Errorf("full revenue CAGR missing")
	}
	// EPS 5y: (19.396910114188728 / 10.259854346620944)^(1/5) - 1
	wantEPS := math.Pow(19.396910114188728/10.259854346620944, 1.0/5) - 1
	near(t, val(t, fy26.Growth.EPSCAGR5Y), wantEPS, 1e-6)

	// Margin stability: net margin trend over 10 years, direction set.
	if doc.Summary.Margins.Net.Mean == nil || doc.Summary.Margins.Net.StdDev == nil || doc.Summary.Margins.Net.Direction == "" {
		t.Errorf("net margin stats incomplete: %+v", doc.Summary.Margins.Net)
	}
	if doc.Summary.Margins.Gross.Mean != nil {
		t.Errorf("gross margin stats should be empty (no gross profit data)")
	}

	// Returns summary.
	if doc.Summary.Returns.ROELatest == nil || doc.Summary.Returns.ROEMean == nil {
		t.Errorf("ROE summary missing")
	}
	// Health summary = FY2026 values.
	near(t, val(t, doc.Summary.Health.CurrentRatio), 102091.11/566940.33, 1e-6)
	// Cashflow summary.
	if doc.Summary.Cashflow.CFOToPATLatest == nil || doc.Summary.Cashflow.CFOToPATMean == nil {
		t.Errorf("CFO/PAT summary missing")
	}
}

func TestGrowthAndCAGREdgeCases(t *testing.T) {
	if growth(ptr(100), ptr(-50)) != nil {
		t.Errorf("growth over negative base must be nil")
	}
	if growth(ptr(100), ptr(0)) != nil {
		t.Errorf("growth over zero base must be nil")
	}
	near(t, val(t, growth(ptr(120), ptr(100))), 0.2, 1e-12)

	if cagr(ptr(100), ptr(50), 0) != nil {
		t.Errorf("cagr with 0 years must be nil")
	}
	if cagr(ptr(-100), ptr(50), 2) != nil {
		t.Errorf("cagr over negative base must be nil")
	}
	near(t, val(t, cagr(ptr(100), ptr(121), 2)), 0.1, 1e-6)

	if ratio(ptr(1), ptr(0)) != nil {
		t.Errorf("ratio with zero denominator must be nil")
	}
	// avgT falls back to current when prior missing; nil when both missing.
	near(t, val(t, avgT(ptr(10), nil)), 10, 1e-12)
	if avgT(nil, ptr(10)) != nil {
		t.Errorf("avgT without current must be nil")
	}
	// sub falls back to a when b missing.
	near(t, val(t, sub(ptr(10), nil)), 10, 1e-12)
	if sub(nil, ptr(1)) != nil {
		t.Errorf("sub without a must be nil")
	}
}

func TestSlopeAndStats(t *testing.T) {
	if slopeOf([]float64{1}) != nil {
		t.Errorf("slope of one point must be nil")
	}
	// Perfectly linear 0, 0.01, 0.02 -> slope 0.01.
	near(t, val(t, slopeOf([]float64{0, 0.01, 0.02})), 0.01, 1e-12)

	ms := marginStats(func(int) *float64 { return nil }, 2)
	if ms.Mean != nil || ms.Direction != "" {
		t.Errorf("stats over no data must be empty")
	}
}

func TestComputeSyntheticManufacturer(t *testing.T) {
	// A non-financial with gross profit, debt, receivables, inventory,
	// payables and interest - exercises ROIC, WC cycle and coverage.
	income := []IncomeRow{
		{DisplayPeriod: "FY 2020", EndDate: "2020-03-31T00:00:00.000Z", Reporting: "standalone",
			TotalRevenue: ptr(1000), GrossProfit: ptr(400), EBITDA: ptr(200), Depreciation: ptr(50),
			PBIT: ptr(150), InterestOther: ptr(30), PBT: ptr(120), TaxOther: ptr(30), NetIncome: ptr(90), EPS: ptr(9)},
		{DisplayPeriod: "FY 2021", EndDate: "2021-03-31T00:00:00.000Z", Reporting: "standalone",
			TotalRevenue: ptr(1100), GrossProfit: ptr(450), EBITDA: ptr(240), Depreciation: ptr(55),
			PBIT: ptr(185), InterestOther: ptr(28), PBT: ptr(157), TaxOther: ptr(39.25), NetIncome: ptr(117.75), EPS: ptr(10.7)},
	}
	balance := []BalanceRow{
		{EndDate: "2020-03-31T00:00:00.000Z", Reporting: "standalone",
			TotalDebt: ptr(300), CashAndSTI: ptr(100), TotalEquity: ptr(500), MinorityInterest: ptr(0),
			CurrentAssets: ptr(250), CurrentLiab: ptr(200), TotalAssets: ptr(800),
			Receivables: ptr(150), Inventory: ptr(120), Payables: ptr(90), SharesOutstanding: ptr(10)},
		{EndDate: "2021-03-31T00:00:00.000Z", Reporting: "standalone",
			TotalDebt: ptr(320), CashAndSTI: ptr(120), TotalEquity: ptr(600), MinorityInterest: ptr(0),
			CurrentAssets: ptr(280), CurrentLiab: ptr(210), TotalAssets: ptr(920),
			Receivables: ptr(180), Inventory: ptr(130), Payables: ptr(100), SharesOutstanding: ptr(10)},
	}
	cashflow := []CashflowRow{
		{EndDate: "2020-03-31T00:00:00.000Z", Reporting: "standalone", CFO: ptr(140), Capex: ptr(60)},
		{EndDate: "2021-03-31T00:00:00.000Z", Reporting: "standalone", CFO: ptr(170), Capex: ptr(80)},
	}

	doc := Compute(BuildStatements("TEST", income, balance, cashflow))
	if doc.Reporting != "standalone" || doc.YearsCount != 2 {
		t.Fatalf("unexpected doc header: %s/%d", doc.Reporting, doc.YearsCount)
	}
	fy21 := doc.Years[1]

	// Gross margin 450/1100.
	near(t, val(t, fy21.Margins.Gross), 450.0/1100, 1e-6)
	// Interest coverage 185/28.
	near(t, val(t, fy21.Health.InterestCoverage), 185.0/28, 1e-6)
	// D/E 320/600.
	near(t, val(t, fy21.Health.DebtToEquity), 320.0/600, 1e-6)
	// Net debt/EBITDA: (320-120)/240.
	near(t, val(t, fy21.Health.NetDebtToEBITDA), 200.0/240, 1e-6)
	// Current ratio 280/210.
	near(t, val(t, fy21.Health.CurrentRatio), 280.0/210, 1e-6)

	// DSO: avg(180,150)/1100*365 = 165/1100*365 (avg + ratio are 6dp-rounded).
	near(t, val(t, fy21.Health.DSO), 165.0/1100*365, 1e-3)
	// DIO: COGS = 1100-450 = 650; avg(130,120)/650*365
	near(t, val(t, fy21.Health.DIO), 125.0/650*365, 1e-3)
	// DPO: avg(100,90)/650*365
	near(t, val(t, fy21.Health.DPO), 95.0/650*365, 1e-3)
	// WC cycle = DSO + DIO - DPO.
	wantCycle := val(t, fy21.Health.DSO) + val(t, fy21.Health.DIO) - val(t, fy21.Health.DPO)
	near(t, val(t, fy21.Health.WCCycleDays), wantCycle, 1e-3)

	// ROIC FY2021: NOPAT = 185*(1-0.25) = 138.75; IC21 = 320+600-120 = 800, IC20 = 300+500-100 = 700; avg = 750.
	near(t, val(t, fy21.Returns.ROIC), 138.75/750, 1e-6)
	// FCF = CFO - capex (no cafFcf given).
	near(t, val(t, fy21.Cashflow.FCF), 170-80, 1e-6)
	// FCF/PAT = 90/117.75.
	near(t, val(t, fy21.Cashflow.FCFToPAT), 90.0/117.75, 1e-6)

	// Payables trend vs COGS.
	near(t, val(t, fy21.Health.PayablesPctCOGS), 100.0/650, 1e-6)
	near(t, val(t, fy21.Health.PayablesYoY), 100.0/90-1, 1e-6)
}

func TestReportingFallbackAndTTMDropped(t *testing.T) {
	// Mixed reporting: consolidated income, standalone balance sheet. The
	// balance rows must survive because no consolidated balance exists.
	income := []IncomeRow{
		{DisplayPeriod: "FY 2020", EndDate: "2020-03-31T00:00:00.000Z", Reporting: "consolidated", TotalRevenue: ptr(100)},
		{DisplayPeriod: "TTM", EndDate: "", Reporting: "", TotalRevenue: ptr(50)},
	}
	balance := []BalanceRow{
		{EndDate: "2020-03-31T00:00:00.000Z", Reporting: "standalone", TotalEquity: ptr(80)},
	}
	st := BuildStatements("MIX", income, balance, nil)
	if st.Reporting != "consolidated" {
		t.Fatalf("reporting = %q", st.Reporting)
	}
	if len(st.Years) != 1 || st.Years[0] != "2020-03-31" {
		t.Fatalf("years = %v (TTM must be dropped)", st.Years)
	}
	if _, ok := st.Balance["2020-03-31"]; !ok {
		t.Fatalf("standalone balance row dropped though no consolidated balance exists")
	}
	if fy := fyLabel(st, "2020-03-31"); fy != "FY 2020" {
		t.Errorf("fy label = %q", fy)
	}
}
