package research

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"dolores/internal/models"
)

// CompanyInfo identifies one company in the research universe and mirrors
// the fields stored in the companies collection.
type CompanyInfo struct {
	Symbol   string
	Exchange string
	Name     string
	Industry string
	Series   string
	ISINCode string
}

// Manufacturing reports whether the company belongs to a manufacturing
// industry. Everything except Financial Services and Information
// Technology is treated as manufacturing.
func (c CompanyInfo) Manufacturing() bool {
	switch c.Industry {
	case "Financial Services", "Information Technology":
		return false
	default:
		return true
	}
}

// InfoFromCompany mirrors a stored Company document into a CompanyInfo.
func InfoFromCompany(co *models.Company) CompanyInfo {
	return CompanyInfo{
		Symbol:   co.Symbol,
		Exchange: co.Exchange,
		Name:     co.Name,
		Industry: co.Industry,
		Series:   co.Series,
		ISINCode: co.ISINCode,
	}
}

// GetCompanyBySymbol returns the company entry for symbol from the
// companies collection (nil when not found). Exchange is always "BSE"
// in this universe, so the lookup is keyed on symbol alone.
func GetCompanyBySymbol(db *mongo.Database, symbol string) (*CompanyInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var co models.Company
	err := db.Collection(models.ColCompanies).FindOne(ctx, bson.M{"symbol": symbol}).Decode(&co)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := InfoFromCompany(&co)
	return &info, nil
}
