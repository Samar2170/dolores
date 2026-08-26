package models

import "gorm.io/gorm"

type CompanyRef struct {
	*gorm.Model
	Symbol   string
	Exchange string
}
