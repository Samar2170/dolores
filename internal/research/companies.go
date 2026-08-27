package research

// CompanyInfo identifies one company in the hardcoded research universe.
type CompanyInfo struct {
	Symbol        string
	Exchange      string
	Name          string
	Manufacturing bool
}

var Universe = []CompanyInfo{
	{Symbol: "WELCORP", Exchange: "BSE", Name: "Welspun Corp", Manufacturing: true},
	{Symbol: "ITC", Exchange: "BSE", Name: "ITC", Manufacturing: true},
	{Symbol: "HDFCBANK", Exchange: "BSE", Name: "HDFC Bank", Manufacturing: false},
	{Symbol: "KOTAKBANK", Exchange: "BSE", Name: "Kotak Mahindra Bank", Manufacturing: false},
}

// BySymbol returns the universe entry for symbol.
func BySymbol(symbol string) (CompanyInfo, bool) {
	for _, c := range Universe {
		if c.Symbol == symbol {
			return c, true
		}
	}
	return CompanyInfo{}, false
}
