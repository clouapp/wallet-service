package ramps

import (
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"time"
)

type Customer struct {
	ID           uuid.UUID `json:"id"`
	ExternalID   string    `json:"external_id"`
	Relationship string    `json:"relationship"`
	EntityType   string    `json:"entity_type"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func NewCustomer(row *models.RampCustomer) Customer {
	return Customer{row.ID, row.ExternalID, row.Relationship, row.EntityType, row.Name, row.CreatedAt, row.UpdatedAt}
}

type Feature struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Effective bool   `json:"effective"`
	Override  *bool  `json:"override"`
}
