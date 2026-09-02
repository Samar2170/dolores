package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	SegmentProducts     = "products"
	SegmentRawInputs    = "inputs_raw_materials"
	SegmentRevenueSplit = "revenue_segmentation"
	SegmentMarketShare  = "market_share"
)

const (
	ColCompanies        = "companies"
	ColCompanyLinks     = "company_links"
	ColCompanyResources = "company_research_resources"
	ColAnalysisFiles    = "analysis_files"
)

type BaseModel struct {
	ID        bson.ObjectID `bson:"_id,omitempty" json:"id"`
	CreatedAt time.Time     `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time     `bson:"updated_at" json:"updated_at"`
}

type Company struct {
	BaseModel `bson:",inline"`
	Symbol    string `bson:"symbol" json:"symbol"`
	Exchange  string `bson:"exchange" json:"exchange"`
	Name      string `bson:"name,omitempty" json:"name,omitempty"`
	Industry  string `bson:"industry,omitempty" json:"industry,omitempty"`
	Series    string `bson:"series,omitempty" json:"series,omitempty"`
	ISINCode  string `bson:"isin_code,omitempty" json:"isin_code,omitempty"`
}

type CompanyLinks struct {
	BaseModel             `bson:",inline"`
	CompanyID             bson.ObjectID `bson:"company_id" json:"company_id"`
	OfficialWebsite       string        `bson:"official_website" json:"official_website"`
	InvestorRelationsLink string        `bson:"investor_relations_link" json:"investor_relations_link"`
}

type CompanyResearchResource struct {
	BaseModel            `bson:",inline"`
	CompanyID            bson.ObjectID `bson:"company_id" json:"company_id"`
	AnalysisSegment      string        `bson:"analysis_segment" json:"analysis_segment"`
	Source               string        `bson:"source" json:"source"`
	ResearchResourceLink string        `bson:"research_resource_link" json:"research_resource_link"`
	RawDataUrl           string        `bson:"raw_data_url" json:"raw_data_url"`
	// extracted data is stored as a JSON document
	ExtractedData bson.M `bson:"extracted_data,omitempty" json:"extracted_data,omitempty"`
}

// AnalysisFile holds the metadata of one file fetched during research and
// archived into the company's research folder. File contents stay in Archivus.
type AnalysisFile struct {
	BaseModel    `bson:",inline"`
	CompanyID    bson.ObjectID `bson:"company_id" json:"company_id"`
	Symbol       string        `bson:"symbol" json:"symbol"`
	Kind         string        `bson:"kind" json:"kind"`
	Title        string        `bson:"title,omitempty" json:"title,omitempty"`
	SourceURL    string        `bson:"source_url" json:"source_url"`
	FY           string        `bson:"fy,omitempty" json:"fy,omitempty"`
	FileName     string        `bson:"file_name" json:"file_name"`
	ArchivusPath string        `bson:"archivus_path" json:"archivus_path"`
	RawDataUrl   string        `bson:"raw_data_url,omitempty" json:"raw_data_url,omitempty"`
	SizeBytes    int64         `bson:"size_bytes" json:"size_bytes"`
}
