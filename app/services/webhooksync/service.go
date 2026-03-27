package webhooksync

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"gorm.io/gorm"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

type Service struct {
	subscriptionRepo repositories.WebhookSubscriptionRepository
	addressRepo      repositories.AddressRepository
	providers        map[string]providers.WebhookProvider
	mu               sync.Map // subscription id -> *sync.Mutex
}

func NewService(subRepo repositories.WebhookSubscriptionRepository, addrRepo repositories.AddressRepository, provs map[string]providers.WebhookProvider) *Service {
	return &Service{subscriptionRepo: subRepo, addressRepo: addrRepo, providers: provs}
}

func (s *Service) lockForSubscription(id uuid.UUID) *sync.Mutex {
	v, _ := s.mu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// SyncChainAddresses loads the active webhook subscription for the chain, serializes syncs per subscription,
// pushes the full active address list to the provider, and updates sync metadata.
func (s *Service) SyncChainAddresses(ctx context.Context, chainID string) error {
	if strings.TrimSpace(chainID) == "" {
		return fmt.Errorf("chainID is required")
	}

	sub, err := s.subscriptionRepo.FindByChainID(chainID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Warn("webhook sync: no active subscription for chain", "chain_id", chainID)
			return nil
		}
		return fmt.Errorf("find webhook subscription: %w", err)
	}
	if sub == nil {
		slog.Warn("webhook sync: no active subscription for chain", "chain_id", chainID)
		return nil
	}

	mtx := s.lockForSubscription(sub.ID)
	mtx.Lock()
	defer mtx.Unlock()

	if err := s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{
		"sync_status": "pending",
	}); err != nil {
		return fmt.Errorf("set sync_status pending: %w", err)
	}

	addresses, err := s.addressRepo.PluckActiveAddresses(chainID)
	if err != nil {
		_ = s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("pluck active addresses: %w", err)
	}

	providerName := strings.ToLower(strings.TrimSpace(sub.Provider))
	provider, ok := s.providers[providerName]
	if !ok {
		_ = s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("unknown webhook provider %q", sub.Provider)
	}

	if _, err := facades.Crypt().DecryptString(sub.SigningSecret); err != nil {
		slog.Error("webhook sync: decrypt signing secret", "subscription_id", sub.ID, "error", err)
		_ = s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("decrypt signing secret: %w", err)
	}

	if err := provider.SyncAddresses(ctx, sub.ProviderWebhookID, addresses); err != nil {
		_ = s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{"sync_status": "failed"})
		return fmt.Errorf("provider sync addresses: %w", err)
	}

	now := time.Now().UTC()
	hash := hashAddresses(addresses)
	if err := s.subscriptionRepo.UpdateFields(sub.ID, map[string]interface{}{
		"sync_status":           "synced",
		"synced_addresses_hash": hash,
		"last_synced_at":        now,
	}); err != nil {
		return fmt.Errorf("update sync success fields: %w", err)
	}

	return nil
}

func hashAddresses(addresses []string) string {
	sorted := append([]string(nil), addresses...)
	sort.Strings(sorted)
	joined := strings.Join(sorted, ",")
	sum := sha256.Sum256([]byte(joined))
	return fmt.Sprintf("%x", sum)
}
