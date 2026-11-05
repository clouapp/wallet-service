package ramps

import (
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// Provider event IDs are independent in sandbox and production.
type environmentEvents struct {
	repositories.WebhookEventRepository
	Mode string
}

func (r environmentEvents) CreateIfNew(e *models.WebhookEvent) (bool, error) {
	e.EventID = r.Mode + ":" + e.EventID
	return r.WebhookEventRepository.CreateIfNew(e)
}
