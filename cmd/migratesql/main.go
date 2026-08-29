package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
	"dolores/internal/store"
)

func parseTime(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
	} {
		if t, err := time.Parse(layout, s.String); err == nil {
			return t.UTC()
		}
	}
	log.Fatalf("unparseable time: %q", s.String)
	return time.Time{}
}

func main() {
	db, err := sql.Open("sqlite3", "storage.db?mode=ro")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	st, err := store.GetStore(".")
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(); err != nil {
		log.Fatalf("migrate indexes: %v", err)
	}

	// companies
	companyIDs := map[int64]bson.ObjectID{}
	ctx := context.Background()
	rows, err := db.Query(`SELECT id, created_at, updated_at, symbol, exchange FROM companies WHERE deleted_at IS NULL`)
	if err != nil {
		log.Fatalf("query companies: %v", err)
	}
	for rows.Next() {
		var id int64
		var created, updated sql.NullString
		var symbol, exchange sql.NullString
		if err := rows.Scan(&id, &created, &updated, &symbol, &exchange); err != nil {
			log.Fatalf("scan company: %v", err)
		}
		c := models.Company{
			Symbol:   symbol.String,
			Exchange: exchange.String,
		}
		c.CreatedAt = parseTime(created)
		c.UpdatedAt = parseTime(updated)
		res, err := st.Collection(models.ColCompanies).InsertOne(ctx, &c)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				log.Printf("[company] %s already exists, skipping", symbol.String)
				var existing models.Company
				if err := st.Collection(models.ColCompanies).FindOne(ctx, bson.M{"symbol": symbol.String, "exchange": exchange.String}).Decode(&existing); err != nil {
					log.Fatalf("lookup existing company %s: %v", symbol.String, err)
				}
				companyIDs[id] = existing.ID
				continue
			}
			log.Fatalf("insert company %s: %v", symbol.String, err)
		}
		companyIDs[id] = res.InsertedID.(bson.ObjectID)
		log.Printf("[company] %s (%s) -> %s", symbol.String, exchange.String, companyIDs[id].Hex())
	}
	rows.Close()

	// company_links
	rows, err = db.Query(`SELECT company_id, created_at, updated_at, official_website, investor_relations_link FROM company_links WHERE deleted_at IS NULL`)
	if err != nil {
		log.Fatalf("query links: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var companyID int64
		var created, updated, official, ir sql.NullString
		if err := rows.Scan(&companyID, &created, &updated, &official, &ir); err != nil {
			log.Fatalf("scan links: %v", err)
		}
		oid, ok := companyIDs[companyID]
		if !ok {
			log.Printf("[links] skipping company_id %d: company not migrated", companyID)
			continue
		}
		l := models.CompanyLinks{
			CompanyID:             oid,
			OfficialWebsite:       official.String,
			InvestorRelationsLink: ir.String,
		}
		l.CreatedAt = parseTime(created)
		l.UpdatedAt = parseTime(updated)
		_, err := st.Collection(models.ColCompanyLinks).UpdateOne(ctx,
			bson.M{"company_id": oid},
			bson.M{"$set": &l},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			log.Fatalf("upsert links for %s: %v", oid.Hex(), err)
		}
		log.Printf("[links] company %s OK", oid.Hex())
	}

	// company_research_resources
	rows, err = db.Query(`SELECT id, company_id, created_at, updated_at, analysis_segment, source, research_resource_link, raw_data_url, extracted_data FROM company_research_resources WHERE deleted_at IS NULL`)
	if err != nil {
		log.Fatalf("query resources: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, companyID int64
		var created, updated, segment, source, rlink, rawurl sql.NullString
		var extracted []byte
		if err := rows.Scan(&id, &companyID, &created, &updated, &segment, &source, &rlink, &rawurl, &extracted); err != nil {
			log.Fatalf("scan resource: %v", err)
		}
		oid, ok := companyIDs[companyID]
		if !ok {
			log.Printf("[resource] skipping row %d: company %d not migrated", id, companyID)
			continue
		}
		var doc bson.M
		if len(extracted) > 0 {
			if err := json.Unmarshal(extracted, &doc); err != nil {
				log.Printf("[resource] row %d: extracted_data not JSON (%v), storing as raw string", id, err)
				doc = bson.M{"_raw": string(extracted)}
			}
		}
		r := models.CompanyResearchResource{
			CompanyID:            oid,
			AnalysisSegment:      segment.String,
			Source:               source.String,
			ResearchResourceLink: rlink.String,
			RawDataUrl:           rawurl.String,
			ExtractedData:        doc,
		}
		r.CreatedAt = parseTime(created)
		r.UpdatedAt = parseTime(updated)
		_, err := st.Collection(models.ColCompanyResources).UpdateOne(ctx,
			bson.M{"company_id": oid, "analysis_segment": segment.String},
			bson.M{"$set": &r},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			log.Fatalf("upsert resource row %d: %v", id, err)
		}
		log.Printf("[resource] %s %s (%d keys) OK", oid.Hex(), segment.String, len(doc))
	}

	fmt.Println("migration complete")
}
