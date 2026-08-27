package models

import "gorm.io/gorm"

const (
	SegmentProducts     = "products"
	SegmentRawInputs    = "inputs_raw_materials"
	SegmentRevenueSplit = "revenue_segmentation"
	SegmentMarketShare  = "market_share"
)

type Company struct {
	*gorm.Model
	Symbol   string `gorm:"uniqueIndex:idx_company_symbol_exchange"`
	Exchange string `gorm:"uniqueIndex:idx_company_symbol_exchange"`
}

type CompanyLinks struct {
	*gorm.Model
	Company               Company `gorm:"foreignKey:CompanyID"`
	CompanyID             uint    `gorm:"uniqueIndex:idx_company_links_company"`
	OfficialWebsite       string
	InvestorRelationsLink string
}

type CompanyResearchResource struct {
	*gorm.Model
	Company              Company `gorm:"foreignKey:CompanyID"`
	CompanyID            uint    `gorm:"uniqueIndex:idx_crr_company_segment"`
	AnalysisSegment      string  `gorm:"uniqueIndex:idx_crr_company_segment"`
	Source               string
	ResearchResourceLink string
	RawDataUrl           string
	// extracted data should be JSONB
	ExtractedData []byte
}
