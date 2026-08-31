package metrics

import (
	"math"
	"time"
)

// Compute derives every feasible metric from the aligned statements. Metrics
// with missing/undefined inputs stay nil.
func Compute(st *Statements) *KeyMetrics {
	doc := &KeyMetrics{
		Symbol:     st.Symbol,
		Reporting:  st.Reporting,
		ComputedAt: time.Now().UTC(),
	}
	doc.UpdatedAt = doc.ComputedAt
	// doc.Notes = defaultNotes()

	for i := range st.Years {
		ym := computeYear(st, i)
		doc.Years = append(doc.Years, ym)
	}
	if len(doc.Years) > 0 {
		doc.FirstFY = doc.Years[0].FY
		doc.LastFY = doc.Years[len(doc.Years)-1].FY
		doc.YearsCount = len(doc.Years)
	}
	computeSummary(st, doc)
	return doc
}

func defaultNotes() []string {
	return []string{
		"all ratio metrics are decimal fractions (0.153 = 15.3%); day counts in days; money in source unit (crores)",
		"volume vs price-led growth and guidance vs actual growth are not computable from the current sources (tickertape financials + Alpha Vantage quotes)",
		"interest_coverage uses tickertape 'Interest & Other Items' (incIoi) and is approximate; null when not reported",
		"working capital day counts use total revenue as COGS proxy when gross profit is not reported",
		"ROIC = NOPAT / average invested capital (debt + equity - cash & ST investments) at the effective tax rate; null when inputs are missing",
	}
}

func computeYear(st *Statements, i int) YearMetrics {
	day := st.Years[i]
	inc := st.Income[day]
	bal := st.Balance[day]
	cf := st.Cashflow[day]

	var prevInc IncomeRow
	var prevBal BalanceRow
	if i > 0 {
		prevDay := st.Years[i-1]
		prevInc = st.Income[prevDay]
		prevBal = st.Balance[prevDay]
	}

	ym := YearMetrics{FY: fyLabel(st, day), EndDate: day}
	ym.Inputs = buildInputs(inc, bal, cf)
	ym.Growth = computeGrowth(st, i, inc, prevInc)
	ym.Margins = computeMargins(inc)
	ym.Returns = computeReturns(inc, bal, prevBal)
	ym.Health = computeHealth(inc, bal, prevBal)
	ym.Cashflow = computeCashflow(inc, cf, ym.Inputs)
	ym.DataGaps = dataGaps(inc, bal, cf)
	return ym
}

func buildInputs(inc IncomeRow, bal BalanceRow, cf CashflowRow) Inputs {
	in := Inputs{
		TotalRevenue:   inc.TotalRevenue,
		GrossProfit:    inc.GrossProfit,
		EBITDA:         inc.EBITDA,
		EBIT:           inc.PBIT,
		PBT:            inc.PBT,
		NetIncome:      inc.NetIncome,
		EPS:            inc.EPS,
		CFO:            cf.CFO,
		Capex:          cf.Capex,
		TotalEquity:    bal.TotalEquity,
		MinorityInt:    bal.MinorityInterest,
		TotalDebt:      bal.TotalDebt,
		CashAndSTI:     bal.CashAndSTI,
		TotalAssets:    bal.TotalAssets,
		CurrentAssets:  bal.CurrentAssets,
		CurrentLiab:    bal.CurrentLiab,
		Receivables:    bal.Receivables,
		Inventory:      bal.Inventory,
		Payables:       bal.Payables,
		SharesOutstand: bal.SharesOutstanding,
	}
	in.NetWorth = sub(bal.TotalEquity, bal.MinorityInterest)
	if f := cf.FCF; f != nil {
		in.FCF = f
	} else {
		in.FCF = sub(cf.CFO, cf.Capex)
	}
	return in
}

// dataGaps lists statement pieces missing for the year, for quick scanning.
func dataGaps(inc IncomeRow, bal BalanceRow, cf CashflowRow) []string {
	var gaps []string
	if inc.TotalRevenue == nil {
		gaps = append(gaps, "income")
	}
	if inc.GrossProfit == nil {
		gaps = append(gaps, "gross_profit")
	}
	if inc.InterestOther == nil {
		gaps = append(gaps, "interest_expense")
	}
	if bal.TotalEquity == nil {
		gaps = append(gaps, "balance_sheet")
	}
	if bal.TotalDebt == nil {
		gaps = append(gaps, "total_debt")
	}
	if bal.Receivables == nil {
		gaps = append(gaps, "receivables")
	}
	if bal.Inventory == nil {
		gaps = append(gaps, "inventory")
	}
	if bal.Payables == nil {
		gaps = append(gaps, "payables")
	}
	if cf.CFO == nil {
		gaps = append(gaps, "cashflow")
	}
	return gaps
}

// --- growth ---

func computeGrowth(st *Statements, i int, inc, prevInc IncomeRow) GrowthMetrics {
	cols := []func(IncomeRow) *float64{
		func(r IncomeRow) *float64 { return r.TotalRevenue },
		func(r IncomeRow) *float64 { return r.EBITDA },
		func(r IncomeRow) *float64 { return r.PBIT },
		func(r IncomeRow) *float64 { return r.NetIncome },
		func(r IncomeRow) *float64 { return r.EPS },
	}

	var g GrowthMetrics
	for idx, fn := range cols {
		cur := fn(inc)
		prev := fn(prevInc)
		yoy := growth(cur, prev)
		c3 := windowCAGR(st.Income, st.Years, i, 3, fn)
		c5 := windowCAGR(st.Income, st.Years, i, 5, fn)
		switch idx {
		case 0:
			g.RevenueYoY, g.RevenueCAGR3Y, g.RevenueCAGR5Y = yoy, c3, c5
		case 1:
			g.EBITDAYoY, g.EBITDACAGR3Y, g.EBITDACAGR5Y = yoy, c3, c5
		case 2:
			g.EBITYoY, g.EBITCAGR3Y, g.EBITCAGR5Y = yoy, c3, c5
		case 3:
			g.PATYoY, g.PATCAGR3Y, g.PATCAGR5Y = yoy, c3, c5
		case 4:
			g.EPSYoY, g.EPSCAGR3Y, g.EPSCAGR5Y = yoy, c3, c5
		}
	}

	// Degree of operating leverage: %ΔEBIT / %ΔRevenue (EBITDA variant too).
	revG := growth(inc.TotalRevenue, prevInc.TotalRevenue)
	g.DOL = leverage(growth(inc.PBIT, prevInc.PBIT), revG)
	g.DOLEBITDA = leverage(growth(inc.EBITDA, prevInc.EBITDA), revG)
	return g
}

// windowCAGR computes the CAGR of one income column over the n years ending at
// index i: (cur/base)^(1/n) - 1.
func windowCAGR(rows map[string]IncomeRow, years []string, i, n int, fn func(IncomeRow) *float64) *float64 {
	j := i - n
	if j < 0 {
		return nil
	}
	return cagr(fn(rows[years[j]]), fn(rows[years[i]]), n)
}

// leverage = %Δprofit / %Δrevenue; nil when either side is undefined.
func leverage(profitGrowth, revenueGrowth *float64) *float64 {
	if profitGrowth == nil || revenueGrowth == nil || *revenueGrowth == 0 {
		return nil
	}
	return fptr(round6(*profitGrowth / *revenueGrowth))
}

// --- margins ---

func computeMargins(inc IncomeRow) MarginMetrics {
	return MarginMetrics{
		Gross:  ratio(inc.GrossProfit, inc.TotalRevenue),
		EBITDA: ratio(inc.EBITDA, inc.TotalRevenue),
		EBIT:   ratio(inc.PBIT, inc.TotalRevenue),
		Net:    ratio(inc.NetIncome, inc.TotalRevenue),
	}
}

// --- returns ---

func computeReturns(inc IncomeRow, bal, prevBal BalanceRow) ReturnMetrics {
	var r ReturnMetrics

	// ROE: PAT / average total equity.
	roe := ratio(inc.NetIncome, avgT(bal.TotalEquity, prevBal.TotalEquity))

	// RONW: PAT / average net worth (equity less minority interest).
	nw := sub(bal.TotalEquity, bal.MinorityInterest)
	prevNW := sub(prevBal.TotalEquity, prevBal.MinorityInterest)
	r.RONW = ratio(inc.NetIncome, avgT(nw, prevNW))

	// ROCE: PBIT / average capital employed (total assets - current
	// liabilities, falling back to equity + debt).
	ce := capitalEmployed(bal)
	prevCE := capitalEmployed(prevBal)
	r.ROCE = ratio(inc.PBIT, avgT(ce, prevCE))

	// ROIC: NOPAT / average invested capital.
	r.ROIC = roic(inc, bal, prevBal)

	r.ROE = roe
	return r
}

func capitalEmployed(bal BalanceRow) *float64 {
	if ce := sub(bal.TotalAssets, bal.CurrentLiab); ce != nil {
		return ce
	}
	return add(bal.TotalEquity, bal.TotalDebt)
}

func roic(inc IncomeRow, bal, prevBal BalanceRow) *float64 {
	taxRate := effectiveTaxRate(inc)
	if taxRate == nil {
		return nil
	}
	pbit := fval(inc.PBIT)
	if pbit == nil {
		return nil
	}
	ic := investedCapital(bal)
	prevIC := investedCapital(prevBal)
	if ic == nil && prevIC == nil {
		return nil
	}
	avgIC := avgT(ic, prevIC)
	if avgIC == nil || *avgIC <= 0 {
		return nil
	}
	nopat := *pbit * (1 - *taxRate)
	return fptr(round6(nopat / *avgIC))
}

// effectiveTaxRate = taxes & other items / PBT, clamped to [0, 1].
func effectiveTaxRate(inc IncomeRow) *float64 {
	tax := fval(inc.TaxOther)
	pbt := fval(inc.PBT)
	if tax == nil || pbt == nil || *pbt <= 0 {
		return nil
	}
	r := *tax / *pbt
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return &r
}

func investedCapital(bal BalanceRow) *float64 {
	if bal.TotalDebt == nil || bal.TotalEquity == nil {
		return nil
	}
	ic := *bal.TotalDebt + *bal.TotalEquity
	if c := fval(bal.CashAndSTI); c != nil {
		ic -= *c
	}
	return &ic
}

// --- balance sheet health ---

func computeHealth(inc IncomeRow, bal, prevBal BalanceRow) HealthMetrics {
	var h HealthMetrics

	h.DebtToEquity = ratio(bal.TotalDebt, bal.TotalEquity)
	netDebt := sub(bal.TotalDebt, bal.CashAndSTI)
	h.NetDebt = netDebt
	if netDebt != nil && inc.EBITDA != nil && *inc.EBITDA > 0 {
		h.NetDebtToEBITDA = fptr(round6(*netDebt / *inc.EBITDA))
	}
	if ioi := fval(inc.InterestOther); ioi != nil && *ioi > 0 {
		if ebit := fval(inc.PBIT); ebit != nil {
			h.InterestCoverage = fptr(round6(*ebit / *ioi))
		}
	}
	h.CurrentRatio = ratio(bal.CurrentAssets, bal.CurrentLiab)

	// Working capital cycle: DSO + DIO - DPO on average balances.
	avgRec := avgT(bal.Receivables, prevBal.Receivables)
	avgInv := avgT(bal.Inventory, prevBal.Inventory)
	avgPay := avgT(bal.Payables, prevBal.Payables)
	cogs := cogsApprox(inc)

	h.DSO = daysRatio(avgRec, inc.TotalRevenue)
	h.DIO = daysRatio(avgInv, cogs)
	h.DPO = daysRatio(avgPay, cogs)
	if h.DSO != nil && h.DIO != nil && h.DPO != nil {
		h.WCCycleDays = fptr(round6(*h.DSO + *h.DIO - *h.DPO))
	}

	// Receivables / inventory / payables trends.
	h.ReceivablesYoY = growth(bal.Receivables, prevBal.Receivables)
	h.ReceivablesPctRev = ratio(bal.Receivables, inc.TotalRevenue)
	h.InventoryYoY = growth(bal.Inventory, prevBal.Inventory)
	h.InventoryPctRev = ratio(bal.Inventory, inc.TotalRevenue)
	h.PayablesYoY = growth(bal.Payables, prevBal.Payables)
	h.PayablesPctCOGS = ratio(bal.Payables, cogs)
	return h
}

// cogsApprox: revenue less gross profit when gross profit exists, else total
// revenue (documented proxy for banks and others without gross profit).
func cogsApprox(inc IncomeRow) *float64 {
	rev := fval(inc.TotalRevenue)
	if rev == nil {
		return nil
	}
	if gp := fval(inc.GrossProfit); gp != nil {
		c := *rev - *gp
		return &c
	}
	return rev
}

func daysRatio(num, den *float64) *float64 {
	r := ratio(num, den)
	if r == nil {
		return nil
	}
	d := round6(*r * 365)
	return &d
}

// --- cash flow ---

func computeCashflow(inc IncomeRow, cf CashflowRow, in Inputs) CashflowMetrics {
	var c CashflowMetrics
	c.CFO = cf.CFO
	c.Capex = cf.Capex
	c.FCF = in.FCF
	c.FCFMargin = ratio(in.FCF, inc.TotalRevenue)
	c.CFOToPAT = ratio(cf.CFO, inc.NetIncome)
	c.FCFToPAT = ratio(in.FCF, inc.NetIncome)
	c.CFOToEBITDA = ratio(cf.CFO, inc.EBITDA)
	return c
}

// --- cross-year summary ---

func computeSummary(st *Statements, doc *KeyMetrics) {
	if len(st.Years) == 0 {
		return
	}
	last := len(st.Years) - 1

	incCol := func(fn func(IncomeRow) *float64) func(int) *float64 {
		return func(i int) *float64 { return fn(st.Income[st.Years[i]]) }
	}
	revenue := incCol(func(r IncomeRow) *float64 { return r.TotalRevenue })
	ebitda := incCol(func(r IncomeRow) *float64 { return r.EBITDA })
	ebit := incCol(func(r IncomeRow) *float64 { return r.PBIT })
	pat := incCol(func(r IncomeRow) *float64 { return r.NetIncome })
	eps := incCol(func(r IncomeRow) *float64 { return r.EPS })

	doc.Summary.CAGR = CAGRSummary{
		Revenue3Y:   windowCAGR(st.Income, st.Years, last, 3, func(r IncomeRow) *float64 { return r.TotalRevenue }),
		Revenue5Y:   windowCAGR(st.Income, st.Years, last, 5, func(r IncomeRow) *float64 { return r.TotalRevenue }),
		RevenueFull: fullCAGR(revenue, last),
		EBITDA3Y:    windowCAGR(st.Income, st.Years, last, 3, func(r IncomeRow) *float64 { return r.EBITDA }),
		EBITDA5Y:    windowCAGR(st.Income, st.Years, last, 5, func(r IncomeRow) *float64 { return r.EBITDA }),
		EBITDAFull:  fullCAGR(ebitda, last),
		EBIT3Y:      windowCAGR(st.Income, st.Years, last, 3, func(r IncomeRow) *float64 { return r.PBIT }),
		EBIT5Y:      windowCAGR(st.Income, st.Years, last, 5, func(r IncomeRow) *float64 { return r.PBIT }),
		EBITFull:    fullCAGR(ebit, last),
		PAT3Y:       windowCAGR(st.Income, st.Years, last, 3, func(r IncomeRow) *float64 { return r.NetIncome }),
		PAT5Y:       windowCAGR(st.Income, st.Years, last, 5, func(r IncomeRow) *float64 { return r.NetIncome }),
		PATFull:     fullCAGR(pat, last),
		EPS3Y:       windowCAGR(st.Income, st.Years, last, 3, func(r IncomeRow) *float64 { return r.EPS }),
		EPS5Y:       windowCAGR(st.Income, st.Years, last, 5, func(r IncomeRow) *float64 { return r.EPS }),
		EPSFull:     fullCAGR(eps, last),
	}

	grossCol := func(i int) *float64 {
		return ratio(st.Income[st.Years[i]].GrossProfit, st.Income[st.Years[i]].TotalRevenue)
	}
	ebitdaCol := func(i int) *float64 { return ratio(st.Income[st.Years[i]].EBITDA, st.Income[st.Years[i]].TotalRevenue) }
	ebitCol := func(i int) *float64 { return ratio(st.Income[st.Years[i]].PBIT, st.Income[st.Years[i]].TotalRevenue) }
	netCol := func(i int) *float64 {
		return ratio(st.Income[st.Years[i]].NetIncome, st.Income[st.Years[i]].TotalRevenue)
	}

	doc.Summary.Margins = MarginStability{
		Gross:  marginStats(grossCol, last),
		EBITDA: marginStats(ebitdaCol, last),
		EBIT:   marginStats(ebitCol, last),
		Net:    marginStats(netCol, last),
	}

	roeCol := yearCol(doc, func(y YearMetrics) *float64 { return y.Returns.ROE })
	ronwCol := yearCol(doc, func(y YearMetrics) *float64 { return y.Returns.RONW })
	roceCol := yearCol(doc, func(y YearMetrics) *float64 { return y.Returns.ROCE })
	roicCol := yearCol(doc, func(y YearMetrics) *float64 { return y.Returns.ROIC })
	doc.Summary.Returns = ReturnSummary{
		ROELatest: roeCol(last), ROEMean: meanOf(collect(roeCol, last)),
		RONWLatest: ronwCol(last), RONWMean: meanOf(collect(ronwCol, last)),
		ROCELatest: roceCol(last), ROCEMean: meanOf(collect(roceCol, last)),
		ROICLatest: roicCol(last), ROICMean: meanOf(collect(roicCol, last)),
	}

	lastHealth := doc.Years[last].Health
	doc.Summary.Health = HealthSummary{
		DebtToEquity:     lastHealth.DebtToEquity,
		NetDebtToEBITDA:  lastHealth.NetDebtToEBITDA,
		InterestCoverage: lastHealth.InterestCoverage,
		CurrentRatio:     lastHealth.CurrentRatio,
		WCCycleDays:      lastHealth.WCCycleDays,
	}

	fcfMarginCol := yearCol(doc, func(y YearMetrics) *float64 { return y.Cashflow.FCFMargin })
	cfoPATCol := yearCol(doc, func(y YearMetrics) *float64 { return y.Cashflow.CFOToPAT })
	doc.Summary.Cashflow = CashflowSummary{
		FCFLatest:       doc.Years[last].Cashflow.FCF,
		FCFMarginLatest: fcfMarginCol(last),
		FCFMarginMean:   meanOf(collect(fcfMarginCol, last)),
		CFOToPATLatest:  cfoPATCol(last),
		CFOToPATMean:    meanOf(collect(cfoPATCol, last)),
	}
}

func yearCol(doc *KeyMetrics, fn func(YearMetrics) *float64) func(int) *float64 {
	return func(i int) *float64 { return fn(doc.Years[i]) }
}

// fullCAGR measures the CAGR from the first to the last year with a usable
// (positive) value at or after the first usable index.
func fullCAGR(col func(int) *float64, last int) *float64 {
	baseIdx, curIdx := -1, -1
	for i := 0; i <= last; i++ {
		if v := fval(col(i)); v != nil && *v > 0 {
			if baseIdx < 0 {
				baseIdx = i
			}
			curIdx = i
		}
	}
	if baseIdx < 0 || curIdx <= baseIdx {
		return nil
	}
	return cagr(col(baseIdx), col(curIdx), curIdx-baseIdx)
}

func marginStats(col func(int) *float64, last int) MarginStats {
	vals := collect(col, last)
	var ms MarginStats
	if len(vals) == 0 {
		return ms
	}
	ms.Mean = meanOf(vals)
	ms.StdDev = stddevOf(vals)
	ms.Min = fptr(round6(minOf(vals)))
	ms.Max = fptr(round6(maxOf(vals)))
	ms.TrendPerYear = slopeOf(vals)
	if ms.TrendPerYear != nil {
		switch {
		case *ms.TrendPerYear >= 0.0025:
			ms.Direction = "rising"
		case *ms.TrendPerYear <= -0.0025:
			ms.Direction = "falling"
		default:
			ms.Direction = "flat"
		}
	}
	return ms
}

func collect(col func(int) *float64, last int) []float64 {
	var vals []float64
	for i := 0; i <= last; i++ {
		if v := fval(col(i)); v != nil {
			vals = append(vals, *v)
		}
	}
	return vals
}

// --- scalar helpers ---

func fval(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func fptr(v float64) *float64 { return &v }

// ratio = num/den; nil when either side is missing or the denominator is 0.
func ratio(num, den *float64) *float64 {
	n := fval(num)
	d := fval(den)
	if n == nil || d == nil || *d == 0 {
		return nil
	}
	return fptr(round6(*n / *d))
}

// growth = cur/prev - 1; nil when the base is missing, zero or negative.
func growth(cur, prev *float64) *float64 {
	c := fval(cur)
	p := fval(prev)
	if c == nil || p == nil || *p <= 0 {
		return nil
	}
	return fptr(round6(*c / *p - 1))
}

// cagr = (cur/base)^(1/years) - 1; nil for non-positive bases.
func cagr(base, cur *float64, years int) *float64 {
	b := fval(base)
	c := fval(cur)
	if b == nil || c == nil || years <= 0 || *b <= 0 || *c <= 0 {
		return nil
	}
	return fptr(round6(math.Pow(*c / *b, 1.0/float64(years)) - 1))
}

// sub = a - b; nil when a is missing (b optional).
func sub(a, b *float64) *float64 {
	x := fval(a)
	if x == nil {
		return nil
	}
	if y := fval(b); y != nil {
		v := round6(*x - *y)
		return &v
	}
	return x
}

// add = a + b; nil when a is missing (b optional).
func add(a, b *float64) *float64 {
	x := fval(a)
	if x == nil {
		return nil
	}
	if y := fval(b); y != nil {
		v := round6(*x + *y)
		return &v
	}
	return x
}

// avgT averages current and prior period values; falls back to the current
// value when the prior period is missing. nil when both are missing.
func avgT(current, prior *float64) *float64 {
	c := fval(current)
	p := fval(prior)
	switch {
	case c == nil:
		return nil
	case p == nil:
		return c
	default:
		v := round6((*c + *p) / 2)
		return &v
	}
}

func round6(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1e6) / 1e6
}

func meanOf(vals []float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	v := round6(sum / float64(len(vals)))
	return &v
}

func stddevOf(vals []float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	m := 0.0
	for _, v := range vals {
		m += v
	}
	m /= float64(len(vals))
	var ss float64
	for _, v := range vals {
		ss += (v - m) * (v - m)
	}
	v := round6(math.Sqrt(ss / float64(len(vals))))
	return &v
}

func minOf(vals []float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxOf(vals []float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// slopeOf is the OLS slope of the series against equally spaced points
// (one step per fiscal year); nil when fewer than two points.
func slopeOf(vals []float64) *float64 {
	n := len(vals)
	if n < 2 {
		return nil
	}
	meanX := float64(n-1) / 2
	meanY := 0.0
	for _, v := range vals {
		meanY += v
	}
	meanY /= float64(n)

	var cov, varX float64
	for i, v := range vals {
		dx := float64(i) - meanX
		cov += dx * (v - meanY)
		varX += dx * dx
	}
	if varX == 0 {
		return nil
	}
	v := round6(cov / varX)
	return &v
}
