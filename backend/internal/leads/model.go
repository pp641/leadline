// Package leads implements lead (company) management: CRUD, filtering, bulk
// actions, tags, timeline and CSV export.
package leads

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

var Statuses = []string{"new", "contacted", "replied", "interested", "qualified", "meeting", "won", "lost", "unsubscribed"}

type Lead struct {
	ID             uuid.UUID       `json:"id"`
	CompanyName    string          `json:"company_name"`
	Website        string          `json:"website"`
	Domain         string          `json:"domain"`
	Industry       string          `json:"industry"`
	Description    string          `json:"description"`
	Country        string          `json:"country"`
	State          string          `json:"state"`
	City           string          `json:"city"`
	Address        string          `json:"address"`
	Phone          string          `json:"phone"`
	BusinessEmail  *string         `json:"business_email"`
	LinkedInURL    string          `json:"linkedin_url"`
	SocialURLs     json.RawMessage `json:"social_urls"`
	EmployeeCount  *int            `json:"employee_count"`
	FoundedYear    *int            `json:"founded_year"`
	Source         string          `json:"source"`
	SourceURL      string          `json:"source_url"`
	ImportID       *uuid.UUID      `json:"import_id"`
	LeadScore      int             `json:"lead_score"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	OwnerID        *uuid.UUID      `json:"owner_id"`
	OwnerName      *string         `json:"owner_name"`
	WebsiteQuality string          `json:"website_quality"`
	TechStack      []string        `json:"tech_stack"`
	ICPFit         *int            `json:"icp_fit"`
	EnrichedAt     *time.Time      `json:"enriched_at"`
	Tags           []string        `json:"tags"`
	PrimaryContact *ContactSummary `json:"primary_contact"`
	LatestCampaign *string         `json:"latest_campaign"`
}

type ContactSummary struct {
	ID          uuid.UUID `json:"id"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	Email       *string   `json:"email"`
	EmailStatus string    `json:"email_status"`
}

// Input is used for create and full update.
type Input struct {
	CompanyName   string   `json:"company_name"`
	Website       string   `json:"website"`
	Industry      string   `json:"industry"`
	Description   string   `json:"description"`
	Country       string   `json:"country"`
	State         string   `json:"state"`
	City          string   `json:"city"`
	Address       string   `json:"address"`
	Phone         string   `json:"phone"`
	BusinessEmail string   `json:"business_email"`
	LinkedInURL   string   `json:"linkedin_url"`
	SocialURLs    []string `json:"social_urls"`
	EmployeeCount *int     `json:"employee_count"`
	FoundedYear   *int     `json:"founded_year"`
	Source        string   `json:"source"`
	SourceURL     string   `json:"source_url"`
	Status        string   `json:"status"`
	Tags          []string `json:"tags"`
	// Optional first contact, created together with the lead.
	Contact *ContactInput `json:"contact,omitempty"`
}

type ContactInput struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	JobTitle    string `json:"job_title"`
	Email       string `json:"email"`
	LinkedInURL string `json:"linkedin_url"`
}

type Event struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	ActorID   *uuid.UUID      `json:"actor_user_id"`
	ActorName *string         `json:"actor_name"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type Facet struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}
