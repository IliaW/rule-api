package model

import "time"

// Rule godoc
// @Description Represents a custom rule for a domain
// @Type Rule
type Rule struct {
	ID        int       `json:"id"`
	Domain    string    `json:"domain"`
	Blocked   bool      `json:"blocked"`
	RobotsTxt string    `json:"robots_txt"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AllowedCrawlResponse godoc
// @Description Is crawl allowed for the domain
// @Type AllowedCrawlResponse
type AllowedCrawlResponse struct {
	IsAllowed  bool   `json:"is_allowed"`
	Blocked    bool   `json:"blocked"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error"`
}

type TargetResponse struct {
	StatusCode int
	Body       []byte
}
