package main

import (
	"context"
	"dolores/fetcher/av"
	fetcher_config "dolores/fetcher/config"
	"dolores/fetcher/indiasm"
	"dolores/storage"
)

func main() {
	symbol := "HDFCBANK"
	exchange := "BSE"
	ctx := context.Background()
	fetcher_config.LoadApiKeys()
	storage := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, "financial_data")
	av := av.New(storage)
	indiasm := indiasm.New(storage)
	_, err := av.TimeSeriesDaily(ctx, symbol+"."+exchange)
	if err != nil {
		panic(err)
	}
	_, err = av.GlobalQuote(ctx, symbol+"."+exchange)
	if err != nil {
		panic(err)
	}
	_, err = indiasm.Stock(ctx, symbol)
	if err != nil {
		panic(err)
	}
}
