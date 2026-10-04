package controllers

import (
	"time"

	"github.com/goravel/framework/support/carbon"
)

// zonedTimestamps shadows the stored timestamps in a response: carbon.DateTime
// marshals as "2006-01-02 15:04:05" without a zone, which browsers read as local time.
type zonedTimestamps struct {
	CreatedAt *time.Time `json:"created_at" swaggertype:"string" format:"date-time"`
	UpdatedAt *time.Time `json:"updated_at" swaggertype:"string" format:"date-time"`
}

func newZonedTimestamps(createdAt, updatedAt *carbon.DateTime) zonedTimestamps {
	return zonedTimestamps{
		CreatedAt: utcTime(createdAt),
		UpdatedAt: utcTime(updatedAt),
	}
}

func utcTime(value *carbon.DateTime) *time.Time {
	if value == nil || value.IsNil() || value.IsZero() {
		return nil
	}
	utc := value.StdTime().UTC()
	return &utc
}
