// Command importnifty loads the Nifty 500 constituents list
// (ind_nifty500list.xlsx) into the companies collection in MongoDB.
//
// The workbook columns are: Company Name, Industry, Symbol, Series,
// ISIN Code. Every row is upserted keyed on (symbol, exchange) with
// exchange hardcoded to "BSE" for all cases. Existing documents keep
// their created_at; new ones get fresh timestamps. Use -dry to preview
// without writing.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
	"dolores/internal/store"
)

const (
	defaultFile    = "ind_nifty500list.xlsx"
	defaultSheet   = "Sheet1"
	exchangeBSE    = "BSE"
	columnsPerRow  = 5
	headerRowCount = 1
)

func main() {
	file := flag.String("file", defaultFile, "path to the nifty500 xlsx file")
	sheet := flag.String("sheet", defaultSheet, "worksheet name to read")
	dry := flag.Bool("dry", false, "list what would be imported without writing")
	flag.Parse()

	st, err := store.GetStore(".")
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		log.Fatalf("migrate indexes: %v", err)
	}

	rows, err := readRows(*file, *sheet)
	if err != nil {
		log.Fatalf("read workbook: %v", err)
	}
	if len(rows) == 0 {
		log.Fatal("no data rows found in workbook")
	}

	if *dry {
		for _, r := range rows {
			log.Printf("[dry] %-12s %-30s %-25s %s %s", r.Symbol, r.Name, r.Industry, r.Series, r.ISINCode)
		}
		log.Printf("done: %d rows would be imported (dry=true)", len(rows))
		return
	}

	if err := importRows(st.Collection(models.ColCompanies), rows); err != nil {
		log.Fatalf("import: %v", err)
	}
	log.Printf("done: %d companies imported/updated", len(rows))
}

// companyRow is one parsed row of the nifty500 workbook.
type companyRow struct {
	Name     string
	Industry string
	Symbol   string
	Series   string
	ISINCode string
}

// readRows opens the workbook and parses all data rows below the header.
func readRows(path, sheet string) ([]companyRow, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("sheet %q: %w", sheet, err)
	}

	var out []companyRow
	for i, row := range rows[headerRowCount:] {
		if len(row) < columnsPerRow {
			// tolerate fully empty tail rows, reject truncated ones
			allEmpty := true
			for _, c := range row {
				if c != "" {
					allEmpty = false
					break
				}
			}
			if allEmpty {
				continue
			}
			return nil, fmt.Errorf("row %d: expected %d columns, got %d", i+headerRowCount+1, columnsPerRow, len(row))
		}
		r := companyRow{
			Name:     row[0],
			Industry: row[1],
			Symbol:   row[2],
			Series:   row[3],
			ISINCode: row[4],
		}
		if r.Symbol == "" {
			log.Printf("[skip] row %d: empty symbol", i+headerRowCount+1)
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// importRows bulk-upserts every row keyed on (symbol, exchange=BSE).
func importRows(coll *mongo.Collection, rows []companyRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	now := time.Now().UTC()
	models := make([]mongo.WriteModel, 0, len(rows))
	for _, r := range rows {
		filter := bson.M{"symbol": r.Symbol, "exchange": exchangeBSE}
		update := bson.M{
			"$set": bson.M{
				"name":       r.Name,
				"industry":   r.Industry,
				"series":     r.Series,
				"isin_code":  r.ISINCode,
				"updated_at": now,
			},
			"$setOnInsert": bson.M{
				"symbol":     r.Symbol,
				"exchange":   exchangeBSE,
				"created_at": now,
			},
		}
		models = append(models, mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(update).SetUpsert(true))
	}

	res, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err != nil {
		return err
	}
	log.Printf("bulk write: upserted=%d matched=%d modified=%d",
		res.UpsertedCount, res.MatchedCount, res.ModifiedCount)
	return nil
}
