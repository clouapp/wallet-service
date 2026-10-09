package walletrecords

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	// ErrWhitelistEntryNotFound is Remove's answer for an entry the wallet does not hold.
	ErrWhitelistEntryNotFound = errors.New("whitelist entry not found")
	// ErrWebhookNotFound is the answer for a webhook the wallet does not hold.
	ErrWebhookNotFound = errors.New("webhook not found")
	// ErrWebhookTestFailed wraps a test delivery the URL did not accept. The
	// cause stays on the error chain.
	ErrWebhookTestFailed = errors.New("webhook test delivery failed")
)

// Add whitelists an address for the wallet.
func (s *Whitelist) Add(ctx context.Context, walletID uuid.UUID, address, label string) (*models.WhitelistEntry, error) {
	entry := &models.WhitelistEntry{
		ID:       uuid.New(),
		WalletID: walletID,
		Address:  address,
		Label:    label,
	}
	if err := s.Create(ctx, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// Remove deletes a whitelist entry of the wallet. An entry the wallet does not
// hold, and a lookup that fails, are ErrWhitelistEntryNotFound.
func (s *Whitelist) Remove(ctx context.Context, walletID, entryID uuid.UUID) error {
	entry, err := s.FindByIDAndWallet(ctx, entryID, walletID)
	if err != nil || entry == nil {
		return ErrWhitelistEntryNotFound
	}
	return s.Delete(ctx, entry)
}

// Register adds a webhook scoped to the wallet.
func (s *Webhooks) Register(ctx context.Context, walletID uuid.UUID, url, secret, events string) (*models.WebhookConfig, error) {
	cfg := &models.WebhookConfig{
		ID:       uuid.New(),
		URL:      url,
		Secret:   secret,
		Events:   events,
		WalletID: &walletID,
		Type:     "wallet",
	}
	if err := s.Create(ctx, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Remove deletes a webhook of the wallet. A webhook the wallet does not hold,
// and a lookup that fails, are ErrWebhookNotFound.
func (s *Webhooks) Remove(ctx context.Context, walletID, webhookID uuid.UUID) error {
	cfg, err := s.FindByIDAndWallet(ctx, webhookID, walletID)
	if err != nil || cfg == nil {
		return ErrWebhookNotFound
	}
	return s.Delete(ctx, cfg)
}

// TestSender posts one signed test delivery to a webhook. *webhook.Service
// implements it.
type TestSender interface {
	SendTest(ctx context.Context, cfg *models.WebhookConfig, walletID uuid.UUID) error
}

// SendTest posts a signed test body to a webhook of the wallet. A webhook the
// wallet does not hold is ErrWebhookNotFound, and a delivery the URL refuses
// ErrWebhookTestFailed.
func (s *Webhooks) SendTest(ctx context.Context, sender TestSender, walletID, webhookID uuid.UUID) error {
	cfg, err := s.FindByIDAndWallet(ctx, webhookID, walletID)
	if err != nil || cfg == nil {
		return ErrWebhookNotFound
	}
	if err := sender.SendTest(ctx, cfg, walletID); err != nil {
		return fmt.Errorf("%w: %w", ErrWebhookTestFailed, err)
	}
	return nil
}
