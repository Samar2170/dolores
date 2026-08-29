package main

import (
	"context"

	"dolores/fetcher/av"
	fetcher_config "dolores/fetcher/config"
	"dolores/internal/market"
	"dolores/internal/store"
	"dolores/storage"
)

func main() {
	symbol := "WELCORP"
	exchange := "BSE"
	ctx := context.Background()
	fetcher_config.LoadApiKeys()

	st, err := store.GetStore(".")
	if err != nil {
		panic(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		panic(err)
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, "financial_data")
	repo := market.NewRepo(st.DB)
	avClient := av.New(arch, repo)
	// indiasmClient := indiasm.New(arch, repo)

	err = arch.EnsureFolder(symbol)
	if err != nil {
		panic(err)
	}
	_, err = avClient.TimeSeriesDaily(ctx, symbol, exchange)
	if err != nil {
		panic(err)
	}
	_, err = avClient.GlobalQuote(ctx, symbol, exchange)
	if err != nil {
		panic(err)
	}
	// _, err = indiasmClient.Stock(ctx, symbol)
	// if err != nil {
	// 	panic(err)
	// }
}
