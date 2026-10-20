package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WebhookSubscriptionRepository persists provider webhook subscriptions.
// signing_secret is written sealed by the caller and returned as stored.
type WebhookSubscriptionRepository struct {
	db.Base
}

// NewWebhookSubscriptionRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWebhookSubscriptionRepository(query orm.Query) *WebhookSubscriptionRepository {
	return &WebhookSubscriptionRepository{Base: db.NewBase(query)}
}

// FindByChainID returns the active subscription for a chain, or ErrRepositoryNotFound.
func (r *WebhookSubscriptionRepository) FindByChainID(ctx context.Context, chainID string) (*models.WebhookSubscription, error) {
	if chainID == "" {
		return nil, models.ErrRepositoryNotFound
	}
	var sub models.WebhookSubscription
	if err := r.Query(ctx).Where("chain_id = ? AND status = ?", chainID, models.WebhookSubscriptionActive).FirstOrFail(&sub); err != nil {
		return nil, db.LookupError(err, "find webhook subscription")
	}
	return &sub, nil
}

// FindByProviderAndChain returns the subscription for a provider and chain, or ErrRepositoryNotFound.
func (r *WebhookSubscriptionRepository) FindByProviderAndChain(ctx context.Context, provider, chainID string) (*models.WebhookSubscription, error) {
	var sub models.WebhookSubscription
	if err := r.Query(ctx).Where("provider = ? AND chain_id = ?", provider, chainID).FirstOrFail(&sub); err != nil {
		return nil, db.LookupError(err, "find webhook subscription")
	}
	return &sub, nil
}

// FindAllActive returns every active subscription.
func (r *WebhookSubscriptionRepository) FindAllActive(ctx context.Context) ([]models.WebhookSubscription, error) {
	var subs []models.WebhookSubscription
	if err := r.Query(ctx).Where("status = ?", models.WebhookSubscriptionActive).Find(&subs); err != nil {
		return nil, fmt.Errorf("list webhook subscriptions: %w", err)
	}
	return subs, nil
}

// FindAll returns every subscription, by chain and provider.
func (r *WebhookSubscriptionRepository) FindAll(ctx context.Context) ([]models.WebhookSubscription, error) {
	var subs []models.WebhookSubscription
	if err := r.Query(ctx).Order("chain_id").Order("provider").Find(&subs); err != nil {
		return nil, fmt.Errorf("list webhook subscriptions: %w", err)
	}
	return subs, nil
}

// Create inserts a subscription, including signing_secret as given.
func (r *WebhookSubscriptionRepository) Create(ctx context.Context, sub *models.WebhookSubscription) error {
	if sub == nil {
		return fmt.Errorf("create webhook subscription: subscription is nil")
	}
	if err := r.Query(ctx).Create(sub); err != nil {
		return fmt.Errorf("create webhook subscription: %w", err)
	}
	return nil
}

// SetSyncStatus sets webhook_subscriptions.sync_status.
func (r *WebhookSubscriptionRepository) SetSyncStatus(ctx context.Context, id uuid.UUID, status string) error {
	if _, err := r.Query(ctx).Model(&models.WebhookSubscription{}).Where("id", id).Update("sync_status", status); err != nil {
		return fmt.Errorf("set webhook subscription sync status: %w", err)
	}
	return nil
}

// RecordSync writes sync_status, synced_addresses_hash, and last_synced_at together.
func (r *WebhookSubscriptionRepository) RecordSync(ctx context.Context, id uuid.UUID, status, hash string, syncedAt time.Time) error {
	_, err := r.Query(ctx).Model(&models.WebhookSubscription{}).Where("id", id).Update(map[string]any{
		"sync_status":           status,
		"synced_addresses_hash": hash,
		"last_synced_at":        syncedAt,
	})
	if err != nil {
		return fmt.Errorf("record webhook subscription sync: %w", err)
	}
	return nil
}
