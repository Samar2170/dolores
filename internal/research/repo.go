package research

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"dolores/internal/models"
)

func MigrateCompanyModels(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.Company{},
		&models.CompanyLinks{},
		&models.CompanyResearchResource{},
	)
}

// UpsertCompany finds or creates the Company row for a symbol+exchange pair.
func UpsertCompany(db *gorm.DB, info CompanyInfo) (*models.Company, error) {
	var co models.Company
	co.Model = &gorm.Model{}
	err := db.Where("symbol = ? AND exchange = ?", info.Symbol, info.Exchange).First(&co).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		co = models.Company{
			Model:    &gorm.Model{},
			Symbol:   info.Symbol,
			Exchange: info.Exchange,
		}
		if err := db.Create(&co).Error; err != nil {
			return nil, err
		}
		return &co, nil
	}
	if err != nil {
		return nil, err
	}
	return &co, nil
}

// SaveLinks upserts the discovered official site + IR link for a company.
func SaveLinks(db *gorm.DB, companyID uint, officialWebsite, irLink string) error {
	var cl models.CompanyLinks
	cl.Model = &gorm.Model{}
	err := db.Where("company_id = ?", companyID).First(&cl).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		cl = models.CompanyLinks{
			Model:                 &gorm.Model{},
			CompanyID:             companyID,
			OfficialWebsite:       officialWebsite,
			InvestorRelationsLink: irLink,
		}
		return db.Create(&cl).Error
	case err != nil:
		return err
	default:
		return db.Model(&cl).Updates(map[string]interface{}{
			"official_website":        officialWebsite,
			"investor_relations_link": irLink,
		}).Error
	}
}

// ResourceInput carries the mutable fields of one CompanyResearchResource row.
type ResourceInput struct {
	CompanyID    uint
	Segment      string
	Source       string
	ResearchLink string
	RawDataUrl   string
}

// UpsertResourcePending creates or refreshes a topic row keyed on
// (company_id, analysis_segment). ExtractedData is left untouched here.
func UpsertResourcePending(db *gorm.DB, in ResourceInput) (*models.CompanyResearchResource, error) {
	var r models.CompanyResearchResource
	r.Model = &gorm.Model{}
	err := db.Where("company_id = ? AND analysis_segment = ?", in.CompanyID, in.Segment).First(&r).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		r = models.CompanyResearchResource{
			Model:                &gorm.Model{},
			CompanyID:            in.CompanyID,
			AnalysisSegment:      in.Segment,
			Source:               in.Source,
			ResearchResourceLink: in.ResearchLink,
			RawDataUrl:           in.RawDataUrl,
		}
		if err := db.Create(&r).Error; err != nil {
			return nil, err
		}
		return &r, nil
	case err != nil:
		return nil, err
	default:
		err = db.Model(&r).Updates(map[string]interface{}{
			"source":                 in.Source,
			"research_resource_link": in.ResearchLink,
			"raw_data_url":           in.RawDataUrl,
		}).Error
		if err != nil {
			return nil, err
		}
		return &r, nil
	}
}

// SetExtractedData stores the JSONB payload for an existing resource row.
func SetExtractedData(db *gorm.DB, resourceID uint, extracted []byte) error {
	return db.Model(&models.CompanyResearchResource{}).
		Where("id = ?", resourceID).
		Update("extracted_data", extracted).Error
}

// GetResource fetches one topic row (nil-safe when absent).
func GetResource(db *gorm.DB, companyID uint, segment string) (*models.CompanyResearchResource, error) {
	var r models.CompanyResearchResource
	r.Model = &gorm.Model{}
	err := db.Where("company_id = ? AND analysis_segment = ?", companyID, segment).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get resource %s: %w", segment, err)
	}
	return &r, nil
}
