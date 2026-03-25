package models

import "time"

type Business struct {
	Name        string    `json:"name"         csv:"name"`
	Category    string    `json:"category"     csv:"category"`
	Street      string    `json:"street"       csv:"street"`
	PostalCode  string    `json:"postal_code"  csv:"postal_code"`
	City        string    `json:"city"         csv:"city"`
	Phone       string    `json:"phone"        csv:"phone"`
	Website     string    `json:"website"      csv:"website"`
	Email       string    `json:"email"        csv:"email"`
	SourceURL   string    `json:"source_url"   csv:"source_url"`
	ScrapedAt   time.Time `json:"scraped_at"   csv:"scraped_at"`
}
