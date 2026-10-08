package ingest

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/app/models"
)

// SubscriptionStore finds the inbound webhook subscription for a provider and chain.
type SubscriptionStore interface {
	FindByProviderAndChain(ctx context.Context, provider, chainID string) (*models.WebhookSubscription, error)
}

// Subscriptions reads inbound webhook subscriptions.
type Subscriptions struct{ store SubscriptionStore }

// NewSubscriptions builds the subscription lookup.
func NewSubscriptions(store SubscriptionStore) *Subscriptions {
	return &Subscriptions{store: store}
}

// FindByProviderAndChain returns the subscription. A missing row is the store's
// not-found error, which the handler already maps to 404.
func (s *Subscriptions) FindByProviderAndChain(ctx context.Context, provider, chainID string) (*models.WebhookSubscription, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find webhook subscription: context is required")
	}
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("ingest subscriptions: webhook subscriptions repository is required")
	}
	return s.store.FindByProviderAndChain(ctx, provider, chainID)
}
