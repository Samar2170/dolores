package metrics

import (
	"sort"
	"strings"
	"time"
)

const dayLayout = "2006-01-02"

// preferredReportings is the reporting basis preference order: consolidated
// statements win when present.
var preferredReportings = []string{"consolidated", "standalone"}

// BuildStatements aligns the three statements on fiscal year end date,
// dropping rows without a parseable endDate (e.g. TTM rows) and selecting one
// reporting basis. Income is the primary statement: its years define the
// timeline; balance sheet / cash flow years are unioned in.
func BuildStatements(symbol string, income []IncomeRow, balance []BalanceRow, cashflow []CashflowRow) *Statements {
	st := &Statements{
		Symbol:   symbol,
		Income:   make(map[string]IncomeRow),
		Balance:  make(map[string]BalanceRow),
		Cashflow: make(map[string]CashflowRow),
	}

	// Keep the latest TTM income row (no endDate, no reporting tag) aside
	// for the valuation snapshot before the reporting filter drops it.
	for i := len(income) - 1; i >= 0; i-- {
		if normDay(income[i].EndDate) == "" && income[i].DisplayPeriod == "TTM" {
			row := income[i]
			st.TTM = &row
			break
		}
	}

	income = filterReporting(income, &st.Reporting)
	balance = filterReporting(balance, &st.Reporting)
	cashflow = filterReporting(cashflow, &st.Reporting)

	years := make(map[string]bool)
	for _, r := range income {
		day := normDay(r.EndDate)
		if day == "" {
			continue
		}
		st.Income[day] = r
		years[day] = true
	}
	for _, r := range balance {
		day := normDay(r.EndDate)
		if day == "" {
			continue
		}
		st.Balance[day] = r
		years[day] = true
	}
	for _, r := range cashflow {
		day := normDay(r.EndDate)
		if day == "" {
			continue
		}
		st.Cashflow[day] = r
		years[day] = true
	}

	st.Years = make([]string, 0, len(years))
	for y := range years {
		st.Years = append(st.Years, y)
	}
	sort.Strings(st.Years)
	return st
}

// pickReporting picks the reporting basis from the values seen.
func pickReporting(seen map[string]int) string {
	for _, p := range preferredReportings {
		if seen[p] > 0 {
			return p
		}
	}
	return ""
}

// filterReporting keeps rows on the preferred reporting basis when that basis
// exists in the set; otherwise it keeps the rows unchanged. It records the
// chosen basis in out (only the first call per statement set sets it).
func filterReporting[T reportingRow](rows []T, out *string) []T {
	if len(rows) == 0 {
		return rows
	}
	seen := make(map[string]int)
	for _, r := range rows {
		seen[r.reporting()]++
	}
	chosen := pickReporting(seen)
	if chosen != "" && *out == "" {
		*out = chosen
	}
	if chosen == "" {
		return rows
	}
	filtered := make([]T, 0, len(rows))
	for _, r := range rows {
		if r.reporting() == chosen {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return rows
	}
	return filtered
}

type reportingRow interface {
	reporting() string
}

func (r IncomeRow) reporting() string   { return r.Reporting }
func (r BalanceRow) reporting() string  { return r.Reporting }
func (r CashflowRow) reporting() string { return r.Reporting }

// normDay normalises a tickertape endDate ("2019-03-31T00:00:00.000Z") to
// "2019-03-31"; "" when unparseable.
func normDay(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) >= len(dayLayout) {
		if t, err := time.Parse(dayLayout, raw[:len(dayLayout)]); err == nil {
			return t.Format(dayLayout)
		}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Format(dayLayout)
	}
	return ""
}

// fyLabel returns the display label for a fiscal year: the statement's own
// displayPeriod when available, else "FY <end year>".
func fyLabel(st *Statements, day string) string {
	if r, ok := st.Income[day]; ok && r.DisplayPeriod != "" {
		return r.DisplayPeriod
	}
	if r, ok := st.Balance[day]; ok && r.DisplayPeriod != "" {
		return r.DisplayPeriod
	}
	if len(day) >= 4 {
		return "FY " + day[:4]
	}
	return day
}
