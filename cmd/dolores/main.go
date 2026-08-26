package main

import (
	"context"
	"dolores/fetcher/av"
	fetcher_config "dolores/fetcher/config"
	"dolores/storage"
)

func main() {
	symbol := "KOTAKBANK"
	exchange := "BSE"
	ctx := context.Background()
	fetcher_config.LoadApiKeys()
	storage := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, "financial_data")
	av := av.New(storage)
	av.TimeSeriesDaily(ctx, symbol+"."+exchange)
}
