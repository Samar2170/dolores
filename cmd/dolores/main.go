package main

import fetcher_config "dolores/fetcher/config"

type CompanyRef struct {
	Symbol   string // HDFCBANK
	Exchange string // NSE
}

func main() {
	fetcher_config.LoadApiKeys()
}
