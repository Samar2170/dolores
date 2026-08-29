// Command migratemarket is a one-time script that moves the market-API JSON
// payloads already archived in Archivus (Alpha Vantage + IndiaSM fetchers)
// into MongoDB via market.Repo.
//
// Filename conventions handled (current and legacy):
//
//	<symbol>_<FUNCTION>_<date>.json        e.g. WELCORP_TIME_SERIES_DAILY_2026-08-27.json
//	<FUNCTION>_<symbol>.<ex>_<date>.json   e.g. GLOBAL_QUOTE_AXISBANK.BSE_2026-08-27.json
//	<symbol>.<ex>_<timestamp>.json         e.g. HDFCBANK.BSE_2026-08-27T00-12-35.json
//	<name>_indiasm_<timestamp>.json        e.g. AXISBANK_indiasm_2026-08-27T16-26-51.json
//	<name>_<timestamp>.json                e.g. HDFCBANK_2026-08-27T00-12-49.json
//
// Only direct children of <parent>/<folder>/ are scanned; the research/
// subfolders and empty legacy scaffolding (alphavantage/, indiasm/...) are
// ignored. Identity is resolved from the payload where possible (AV embeds
// "WELCORP.BSE" and the refresh date), falling back to the filename. The run
// is idempotent: market.Repo upserts per (symbol/name, day). Use -dry to
// preview without writing.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	fetcher_config "dolores/fetcher/config"
	"dolores/internal/market"
	"dolores/internal/store"
	"dolores/storage"
)

const (
	markerIndiasm = "_indiasm_"

	dateFormat = "2006-01-02"
	stampTz    = "2006-01-02T15-04-05"
)

// fileRef is one Archivus payload file resolved for migration.
type fileRef struct {
	symbol   string
	exchange string
	kind     string
	day      string // YYYY-MM-DD
	raw      []byte
}

func main() {
	dry := flag.Bool("dry", false, "list what would be migrated without writing")
	flag.Parse()

	ctx := context.Background()
	if err := fetcher_config.LoadDefaultConfigs(); err != nil {
		log.Fatalf("load config: %v", err)
	}

	st, err := store.GetStore(".")
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		log.Fatalf("migrate indexes: %v", err)
	}

	arch := storage.NewArchivusClient(fetcher_config.ARCHIVUS_API_KEY, fetcher_config.StorageParentFolder()).WithTimeout(2 * time.Minute)
	repo := market.NewRepo(st.DB)

	dirs, err := arch.List("")
	if err != nil {
		log.Fatalf("list archivus root: %v", err)
	}

	var migrated, skipped int
	for _, dir := range dirs {
		if !dir.IsDir {
			continue
		}
		log.Printf("[scan] folder %s", dir.Name)

		entries, err := arch.List(dir.Name)
		if err != nil {
			log.Fatalf("list folder %s: %v", dir.Name, err)
		}
		for _, e := range entries {
			if e.IsDir || e.ID == "" || !strings.HasSuffix(strings.ToLower(e.Name), ".json") {
				continue
			}
			ref, err := load(arch, dir.Name, e)
			if err != nil {
				log.Printf("[skip] %s/%s: %v", dir.Name, e.Name, err)
				skipped++
				continue
			}

			if *dry {
				log.Printf("[dry] %s/%s kind=%s symbol=%s exchange=%q day=%s (%d bytes)",
					dir.Name, e.Name, ref.kind, ref.symbol, ref.exchange, ref.day, len(ref.raw))
				migrated++
				continue
			}
			if err := repo.SaveRaw(ctx, ref.kind, ref.symbol, ref.exchange, ref.day, ref.raw); err != nil {
				log.Fatalf("save %s/%s: %v", dir.Name, e.Name, err)
			}
			log.Printf("[ok] %s/%s -> %s (%s %s)", dir.Name, e.Name, ref.kind, ref.symbol, ref.day)
			migrated++
		}
	}

	log.Printf("done: %d migrated, %d skipped (dry=%v)", migrated, skipped, *dry)
}

// load downloads one Archivus JSON file and resolves its identity (kind,
// symbol, exchange, day) from a mix of filename convention and payload
// contents.
func load(arch *storage.ArchivusClient, folder string, entry storage.FileInfo) (*fileRef, error) {
	stem := strings.TrimSuffix(entry.Name, ".json")

	hint, fnameSymbol, stamp, err := parseFileName(stem)
	if err != nil {
		return nil, err
	}

	raw, _, err := arch.Download(entry.ID)
	if err != nil {
		return nil, err
	}

	kind := sniffKind(raw)
	if kind == "" {
		kind = hint
	}
	if kind == "" {
		return nil, fmt.Errorf("could not determine payload kind")
	}

	day := payloadDay(kind, raw)
	if day == "" {
		if stamp == "" {
			return nil, fmt.Errorf("no fetch day available")
		}
		day = stamp
	}

	ref := &fileRef{kind: kind, day: day, raw: raw}
	switch kind {
	case market.KindTimeSeriesDaily, market.KindGlobalQuote:
		ref.symbol, ref.exchange = splitAPI(payloadAVSymbol(kind, raw))
		if ref.symbol == "" {
			ref.symbol, ref.exchange = splitAPI(fnameSymbol)
		}
	default: // market.KindStock
		ref.symbol = fnameSymbol
	}
	if ref.symbol == "" {
		return nil, fmt.Errorf("no symbol available")
	}
	return ref, nil
}

// parseFileName extracts a kind hint, the filename symbol and a normalised
// day stamp from the stem, supporting all past fetcher naming conventions.
func parseFileName(stem string) (kind, symbol, stamp string, err error) {
	tokens := []struct {
		tok  string
		kind string
	}{
		{"TIME_SERIES_DAILY", market.KindTimeSeriesDaily},
		{"GLOBAL_QUOTE", market.KindGlobalQuote},
	}

	// <symbol>_<FUNCTION>_<date> (current av convention)
	for _, m := range tokens {
		if idx := strings.LastIndex(stem, "_"+m.tok+"_"); idx >= 0 {
			day, perr := normalizeStamp(stem[idx+len(m.tok)+2:])
			if perr != nil {
				return "", "", "", perr
			}
			return m.kind, stem[:idx], day, nil
		}
	}
	// <FUNCTION>_<symbol>_<date> (legacy av convention)
	for _, m := range tokens {
		if strings.HasPrefix(stem, m.tok+"_") {
			rest := stem[len(m.tok)+1:]
			idx := strings.LastIndex(rest, "_")
			if idx < 0 {
				return "", "", "", fmt.Errorf("malformed legacy filename")
			}
			day, perr := normalizeStamp(rest[idx+1:])
			if perr != nil {
				return "", "", "", perr
			}
			return m.kind, rest[:idx], day, nil
		}
	}
	// <name>_indiasm_<timestamp>
	if idx := strings.LastIndex(stem, markerIndiasm); idx >= 0 {
		day, perr := normalizeStamp(stem[idx+len(markerIndiasm):])
		if perr != nil {
			return "", "", "", perr
		}
		return market.KindStock, stem[:idx], day, nil
	}
	// <name>[_.<exchange>]_<timestamp> (legacy av/indiasm without marker)
	idx := strings.LastIndex(stem, "_")
	if idx < 0 {
		return "", "", "", fmt.Errorf("unrecognised filename")
	}
	day, perr := normalizeStamp(stem[idx+1:])
	if perr != nil {
		return "", "", "", perr
	}
	return "", stem[:idx], day, nil
}

// normalizeStamp parses a date or timestamp token and returns YYYY-MM-DD.
func normalizeStamp(s string) (string, error) {
	if t, err := time.Parse(stampTz, s); err == nil {
		return t.Format(dateFormat), nil
	}
	t, err := time.Parse(dateFormat, s)
	if err != nil {
		return "", fmt.Errorf("parse stamp %q: %w", s, err)
	}
	return t.Format(dateFormat), nil
}

// sniffKind determines the payload kind from its top-level shape. Returns ""
// when unknown (rate-limit notes, garbage, ...).
func sniffKind(raw []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}
	if _, ok := top["Time Series (Daily)"]; ok {
		return market.KindTimeSeriesDaily
	}
	if _, ok := top["Global Quote"]; ok {
		return market.KindGlobalQuote
	}
	if _, ok := top["companyName"]; ok {
		return market.KindStock
	}
	return ""
}

// payloadDay extracts the fetch day from an Alpha Vantage payload
// (last refreshed / latest trading day). "" when unavailable.
func payloadDay(kind string, raw []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}

	var day string
	switch kind {
	case market.KindTimeSeriesDaily:
		var md struct {
			Refreshed string `json:"3. Last Refreshed"`
		}
		if json.Unmarshal(top["Meta Data"], &md) == nil {
			day = md.Refreshed
		}
	case market.KindGlobalQuote:
		var gq struct {
			LatestDay string `json:"07. latest trading day"`
		}
		if json.Unmarshal(top["Global Quote"], &gq) == nil {
			day = gq.LatestDay
		}
	}

	parsed, err := normalizeStamp(strings.TrimSpace(day))
	if err != nil {
		return ""
	}
	return parsed
}

// splitAPI splits an Alpha Vantage API symbol ("WELCORP.BSE" -> "WELCORP",
// "BSE"); a plain symbol yields an empty exchange.
func splitAPI(apiSymbol string) (symbol, exchange string) {
	if idx := strings.LastIndex(apiSymbol, "."); idx > 0 {
		return apiSymbol[:idx], apiSymbol[idx+1:]
	}
	return apiSymbol, ""
}

// payloadAVSymbol reads the full API symbol out of an Alpha Vantage payload.
func payloadAVSymbol(kind string, raw []byte) string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return ""
	}

	switch kind {
	case market.KindTimeSeriesDaily:
		var md struct {
			Symbol string `json:"2. Symbol"`
		}
		if json.Unmarshal(top["Meta Data"], &md) == nil {
			return md.Symbol
		}
	case market.KindGlobalQuote:
		var gq struct {
			Symbol string `json:"01. symbol"`
		}
		if json.Unmarshal(top["Global Quote"], &gq) == nil {
			return gq.Symbol
		}
	}
	return ""
}
