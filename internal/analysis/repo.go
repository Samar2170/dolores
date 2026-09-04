// Repo-side plumbing: persist the financial-analysis report into the
// company_financial_analysis collection.

package analysis

import (
	"context"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
)

const dayFormat = "2006-01-02"

// Migrate ensures the company_financial_analysis collection and its indexes
// exist: unique (symbol, day) for upserts plus a lookup index on company_id.
func Migrate(db *mongo.Database) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.Collection(models.ColCompanyFinancialAnalysis).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "day", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_cfa_symbol_day"),
	}); err != nil {
		return err
	}
	_, err := db.Collection(models.ColCompanyFinancialAnalysis).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "company_id", Value: 1}},
		Options: options.Index().SetName("idx_cfa_company"),
	})
	return err
}

// SaveReport upserts one agent report into company_financial_analysis, keyed
// on (symbol, day): re-running on the same day updates in place, reports from
// earlier days stay as history. sources lists the stored collections the
// analysis was built from.
func SaveReport(ctx context.Context, db *mongo.Database, data *Data, report string, llmRequests, llmTokens int) (string, error) {
	day := time.Now().Format(dayFormat)
	now := time.Now().UTC()

	sources := make([]string, 0, len(data.Sources))
	for name := range data.Sources {
		sources = append(sources, name)
	}
	sort.Strings(sources)

	_, err := db.Collection(models.ColCompanyFinancialAnalysis).UpdateOne(ctx,
		bson.M{"symbol": data.Symbol, "day": day},
		bson.M{
			"$set": bson.M{
				"company_id":   data.Company.CompanyID,
				"report":       report,
				"sources":      sources,
				"llm_requests": llmRequests,
				"llm_tokens":   llmTokens,
				"updated_at":   now,
			},
			"$setOnInsert": bson.M{"created_at": now},
		},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return "", err
	}
	return day, nil
}