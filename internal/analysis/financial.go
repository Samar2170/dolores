package analysis

import (
	"context"
	"dolores/internal/fetcher/tickertape"
	"dolores/internal/models"
	"dolores/internal/storage"
	"dolores/internal/store"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type statementDoc struct {
	Payload []map[string]interface{} `bson:"payload"`
}

type FinancialAnalysis struct {
	store   *store.Store
	storage *storage.ArchivusClient
	Company models.Company
}

type FinancialStatement struct {
	Length          int
	IncomeStatement []map[string]interface{}
	BalanceSheet    []map[string]interface{}
	CashFlow        []map[string]interface{}
	KeyMetrics      map[string]interface{}
}

func (fs *FinancialStatement) IsEmpty() bool {
	return fs.Length == 0
}

func (fs *FinancialStatement) String() string {
	return "Income Statement: " + formatStatement(fs.IncomeStatement) +
		"\nBalance Sheet: " + formatStatement(fs.BalanceSheet) +
		"\nCash Flow: " + formatStatement(fs.CashFlow)
	// "\nKey Metrics: " + formatKeyMetrics(fs.KeyMetrics)
}

func formatStatement(statement []map[string]interface{}) string {
	result := ""
	for _, row := range statement {
		for k, v := range row {
			result += k + ": " + toString(v) + ", "
		}
		result = result[:len(result)-2] // Remove trailing comma and space
		result += "\n"
	}
	return result
}

func toString(value interface{}) string {
	switch v := value.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.Format("2006-01-02")
		}
		return v
	case time.Time:
		return v.Format("2006-01-02")
	case bson.DateTime:
		return v.Time().Format("2006-01-02")
	case float64:
		return fmt.Sprintf("%.2f", v)
	case int:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func NewFinancialAnalysis(store *store.Store, storage *storage.ArchivusClient, company models.Company) *FinancialAnalysis {
	return &FinancialAnalysis{
		store:   store,
		storage: storage,
		Company: company,
	}
}

func (fa *FinancialAnalysis) Run() error {
	// Implement the financial analysis logic here
	return nil
}

func (fa *FinancialAnalysis) LoadData(ctx context.Context) (FinancialStatement, error) {
	statements, err := fa.GetStatements(ctx)
	if err != nil {
		return FinancialStatement{}, err
	}

	if statements.IsEmpty() {
		return FinancialStatement{}, fmt.Errorf("no financial statements found for company: %s", fa.Company.Symbol)
	}
	keyMetrics, err := fa.GetKeyMetrics(ctx)
	if err != nil {
		return FinancialStatement{}, err
	}

	fmt.Println("Financial Statements for company:", fa.Company.Symbol)
	fmt.Println(statements.String())
	fmt.Println("Key Metrics:", keyMetrics)
	return statements, nil
}

func (fa *FinancialAnalysis) GetKeyMetrics(ctx context.Context) (map[string]interface{}, error) {
	key_metrics_row := fa.store.DB.Collection(models.ColKeyMetrics).FindOne(ctx, bson.M{
		"company_id": fa.Company.ID,
	}, options.FindOne().SetProjection(bson.M{"years": 1, "valuation": 1, "source_notes": 1, "summary": 1}))
	var key_metrics map[string]interface{}
	err := key_metrics_row.Decode(&key_metrics)
	if err != nil {
		return nil, err
	}
	return key_metrics, nil
}

func (fa *FinancialAnalysis) GetStatements(ctx context.Context) (FinancialStatement, error) {
	// Implement the logic to load data for financial analysis here
	income_data := fa.store.DB.Collection(models.ColTickerIncomeAnnual).FindOne(ctx, bson.M{
		"company_id": fa.Company.ID,
	}, options.FindOne().SetProjection(bson.M{"payload": 1}))
	var income_statement statementDoc
	err := income_data.Decode(&income_statement)
	if err != nil {
		return FinancialStatement{}, err
	}

	balance_sheet := fa.store.DB.Collection(models.ColTickerBalancesheetAnnual).FindOne(ctx, bson.M{
		"company_id": fa.Company.ID,
	}, options.FindOne().SetProjection(bson.M{"payload": 1}))
	var balance_sheet_statement statementDoc
	err = balance_sheet.Decode(&balance_sheet_statement)
	if err != nil {
		return FinancialStatement{}, err
	}

	cash_flow := fa.store.DB.Collection(models.ColTickerCashflowAnnual).FindOne(ctx, bson.M{
		"company_id": fa.Company.ID,
	}, options.FindOne().SetProjection(bson.M{"payload": 1}))
	var cash_flow_statement statementDoc
	err = cash_flow.Decode(&cash_flow_statement)
	if err != nil {
		return FinancialStatement{}, err
	}
	income_statement.Payload = filterData(income_statement.Payload)
	balance_sheet_statement.Payload = filterData(balance_sheet_statement.Payload)
	cash_flow_statement.Payload = filterData(cash_flow_statement.Payload)

	return FinancialStatement{
		Length:          len(income_statement.Payload),
		IncomeStatement: income_statement.Payload,
		BalanceSheet:    balance_sheet_statement.Payload,
		CashFlow:        cash_flow_statement.Payload,
	}, nil

}

func filterData(data []map[string]interface{}) []map[string]interface{} {
	for i, row := range data {
		swapped := tickertape.SwapKeys(row)
		for k, v := range swapped {
			if v == nil {
				delete(swapped, k)
			}
		}
		data[i] = swapped
	}
	return data
}
