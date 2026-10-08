package chains

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// ChainResource is the chain resource the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps. A nil page stays
// nil; an empty page stays empty. A non-nil empty description stays "".
type ChainResource struct {
	CreatedAt    *carbon.DateTime `json:"created_at"`
	UpdatedAt    *carbon.DateTime `json:"updated_at"`
	ID           uuid.UUID        `json:"id"`
	ChainID      string           `json:"chain_id"`
	Type         string           `json:"type"`
	Name         string           `json:"name"`
	URL          string           `json:"url"`
	Description  *string          `json:"description,omitempty"`
	DisplayOrder int              `json:"display_order"`
	Status       string           `json:"status"`
}

// ChainResourceFrom projects one chain resource.
func ChainResourceFrom(resource models.ChainResource) ChainResource {
	return ChainResource{
		CreatedAt:    resource.CreatedAt,
		UpdatedAt:    resource.UpdatedAt,
		ID:           resource.ID,
		ChainID:      resource.ChainID,
		Type:         resource.Type,
		Name:         resource.Name,
		URL:          resource.URL,
		Description:  resource.Description,
		DisplayOrder: resource.DisplayOrder,
		Status:       resource.Status,
	}
}

// ChainResourcesFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func ChainResourcesFrom(resources []models.ChainResource) []ChainResource {
	if resources == nil {
		return nil
	}
	views := make([]ChainResource, len(resources))
	for i := range resources {
		views[i] = ChainResourceFrom(resources[i])
	}
	return views
}
