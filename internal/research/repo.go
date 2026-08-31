package research

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"dolores/internal/models"
)

var ErrRecordNotFound = mongo.ErrNoDocuments

func now() time.Time { return time.Now().UTC() }

// MigrateCompanyModels ensures collections + indexes exist.
func MigrateCompanyModels(db *mongo.Database) error {
	_, err := db.Collection(models.ColCompanies).Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "symbol", Value: 1}, {Key: "exchange", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_company_symbol_exchange"),
		},
	})
	if err != nil {
		return err
	}

	_, err = db.Collection(models.ColCompanyLinks).Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "company_id", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_company_links_company"),
	})
	if err != nil {
		return err
	}

	_, err = db.Collection(models.ColCompanyResources).Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "company_id", Value: 1}, {Key: "analysis_segment", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_crr_company_segment"),
	})
	return err
}

// UpsertCompany finds or creates the Company document for a symbol+exchange pair.
func UpsertCompany(db *mongo.Database, info CompanyInfo) (*models.Company, error) {
	coll := db.Collection(models.ColCompanies)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var co models.Company
	err := coll.FindOne(ctx, bson.M{"symbol": info.Symbol, "exchange": info.Exchange}).Decode(&co)
	if errors.Is(err, mongo.ErrNoDocuments) {
		co = models.Company{
			Symbol:   info.Symbol,
			Exchange: info.Exchange,
			Name:     info.Name,
			Industry: info.Industry,
			Series:   info.Series,
			ISINCode: info.ISINCode,
		}
		co.CreatedAt = now()
		co.UpdatedAt = co.CreatedAt
		res, ierr := coll.InsertOne(ctx, &co)
		if ierr != nil {
			return nil, ierr
		}
		co.ID = res.InsertedID.(bson.ObjectID)
		return &co, nil
	}
	if err != nil {
		return nil, err
	}
	return &co, nil
}

// SaveLinks upserts the discovered official site + IR link for a company.
func SaveLinks(db *mongo.Database, companyID bson.ObjectID, officialWebsite, irLink string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := db.Collection(models.ColCompanyLinks).UpdateOne(ctx,
		bson.M{"company_id": companyID},
		bson.M{"$set": bson.M{
			"official_website":        officialWebsite,
			"investor_relations_link": irLink,
		}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

// ResourceInput carries the mutable fields of one CompanyResearchResource document.
type ResourceInput struct {
	CompanyID    bson.ObjectID
	Segment      string
	Source       string
	ResearchLink string
	RawDataUrl   string
}

// UpsertResourcePending creates or refreshes a topic document keyed on
// (company_id, analysis_segment). ExtractedData is left untouched here.
func UpsertResourcePending(db *mongo.Database, in ResourceInput) (*models.CompanyResearchResource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	coll := db.Collection(models.ColCompanyResources)

	var r models.CompanyResearchResource
	err := coll.FindOne(ctx, bson.M{"company_id": in.CompanyID, "analysis_segment": in.Segment}).Decode(&r)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		r = models.CompanyResearchResource{
			CompanyID:            in.CompanyID,
			AnalysisSegment:      in.Segment,
			Source:               in.Source,
			ResearchResourceLink: in.ResearchLink,
			RawDataUrl:           in.RawDataUrl,
		}
		r.CreatedAt = now()
		r.UpdatedAt = r.CreatedAt
		res, ierr := coll.InsertOne(ctx, &r)
		if ierr != nil {
			return nil, ierr
		}
		r.ID = res.InsertedID.(bson.ObjectID)
		return &r, nil
	case err != nil:
		return nil, err
	default:
		_, uerr := coll.UpdateOne(ctx,
			bson.M{"_id": r.ID},
			bson.M{"$set": bson.M{
				"source":                 in.Source,
				"research_resource_link": in.ResearchLink,
				"raw_data_url":           in.RawDataUrl,
				"updated_at":             now(),
			}},
		)
		if uerr != nil {
			return nil, uerr
		}
		r.Source = in.Source
		r.ResearchResourceLink = in.ResearchLink
		r.RawDataUrl = in.RawDataUrl
		return &r, nil
	}
}

// SetExtractedData stores the JSON payload as a document on an existing resource.
func SetExtractedData(db *mongo.Database, resourceID bson.ObjectID, extracted []byte) error {
	var doc bson.M
	if err := json.Unmarshal(extracted, &doc); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := db.Collection(models.ColCompanyResources).UpdateOne(ctx,
		bson.M{"_id": resourceID},
		bson.M{"$set": bson.M{"extracted_data": doc, "updated_at": now()}},
	)
	return err
}

// GetResource fetches one topic document (nil-safe when absent).
func GetResource(db *mongo.Database, companyID bson.ObjectID, segment string) (*models.CompanyResearchResource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var r models.CompanyResearchResource
	err := db.Collection(models.ColCompanyResources).FindOne(ctx,
		bson.M{"company_id": companyID, "analysis_segment": segment},
	).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetResourceJSON returns the extracted data of a resource re-marshaled to JSON.
func GetResourceJSON(db *mongo.Database, companyID bson.ObjectID, segment string) ([]byte, error) {
	r, err := GetResource(db, companyID, segment)
	if err != nil || r == nil || r.ExtractedData == nil {
		return nil, err
	}
	out, err := json.Marshal(r.ExtractedData)
	if err != nil {
		log.Printf("[repo] marshal extracted data: %v", err)
		return nil, err
	}
	return out, nil
}
