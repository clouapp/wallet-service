package models

import "time"

const (
	RequestLogAudienceClient  = "client"
	RequestLogAudienceAdmin   = "admin"
	RequestLogAudienceWebhook = "webhook"
)

// RequestLog is an append-only HTTP capture row for the admin explorer.
type RequestLog struct {
	ID         uint    `gorm:"primaryKey" json:"id"`
	RequestID  string  `gorm:"type:uuid;not null;index" json:"request_id"`
	Audience   string  `gorm:"size:16;not null" json:"audience"`
	Method     string  `gorm:"size:8;not null" json:"method"`
	Path       string  `gorm:"size:512;not null;index" json:"path"`
	Query      string  `gorm:"type:jsonb;not null" json:"-"`
	Status     int     `gorm:"not null;index" json:"status"`
	DurationMs int     `gorm:"not null" json:"duration_ms"`
	ClientIP   *string `gorm:"size:45" json:"client_ip,omitempty"`
	UserID     *uint   `gorm:"index" json:"user_id,omitempty"`
	// CustomerID is the customer a client request acted for, beside UserID,
	// who asked; nil outside the customer scope.
	CustomerID      *uint     `gorm:"index" json:"customer_id,omitempty"`
	AdminID         *uint     `gorm:"index" json:"admin_id,omitempty"`
	RequestHeaders  string    `gorm:"type:jsonb;not null" json:"-"`
	RequestBody     *string   `gorm:"type:text" json:"-"`
	ResponseHeaders string    `gorm:"type:jsonb;not null" json:"-"`
	ResponseBody    *string   `gorm:"type:text" json:"-"`
	Error           *string   `gorm:"type:text" json:"error,omitempty"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}
