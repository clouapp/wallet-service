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

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/types"
)

// subscriptionStore is the provider-subscription persistence this service uses.
type subscriptionStore interface {
	FindByChainID(ctx context.Context, chainID string) (*models.WebhookSubscription, error)
	FindAllActive(ctx context.Context) ([]models.WebhookSubscription, error)
	SetSyncStatus(ctx context.Context, id uuid.UUID, status string) error
	RecordSync(ctx context.Context, id uuid.UUID, status, hash string, syncedAt time.Time) error
}

type Service struct {
	subscriptionRepo subscriptionStore
	addressRepo      *repositories.AddressRepository
	providers        map[string]providers.WebhookProvider
	mu               sync.Map // subscription id -> *sync.Mutex
}

func NewService(subRepo subscriptionStore, addrRepo *repositories.AddressRepository, provs map[string]providers.WebhookProvider) *Service {
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

	sub, err := s.subscriptionRepo.FindByChainID(ctx, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Warn("webhook sync: no active subscription for chain", "chain_id", chainID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("find webhook subscription: %w", err)
	}
	if sub == nil {
		slog.Warn("webhook sync: no active subscription for chain", "chain_id", chainID)
		return nil
	}

	mtx := s.lockForSubscription(sub.ID)
	mtx.Lock()
	defer mtx.Unlock()

	if err := s.subscriptionRepo.SetSyncStatus(ctx, sub.ID, string(types.SyncStatusSyncing)); err != nil {
		return fmt.Errorf("set sync_status pending: %w", err)
	}

	addresses, err := s.addressRepo.PluckActiveAddresses(ctx, chainID)
	if err != nil {
		s.markFailed(ctx, sub.ID)
		return fmt.Errorf("pluck active addresses: %w", err)
	}

	providerName := strings.ToLower(strings.TrimSpace(sub.Provider))
	provider, ok := s.providers[providerName]
	if !ok {
		s.markFailed(ctx, sub.ID)
		return fmt.Errorf("unknown webhook provider %q", sub.Provider)
	}

	if _, err := facades.Crypt().DecryptString(sub.SigningSecret); err != nil {
		slog.Error("webhook sync: decrypt signing secret", "subscription_id", sub.ID, "error", err)
		s.markFailed(ctx, sub.ID)
		return fmt.Errorf("decrypt signing secret: %w", err)
	}

	if err := provider.SyncAddresses(ctx, sub.ProviderWebhookID, addresses); err != nil {
		s.markFailed(ctx, sub.ID)
		return fmt.Errorf("provider sync addresses: %w", err)
	}

	now := time.Now().UTC()
	hash := hashAddresses(addresses)
	if err := s.subscriptionRepo.RecordSync(ctx, sub.ID, string(types.SyncStatusSynced), hash, now); err != nil {
		return fmt.Errorf("update sync success fields: %w", err)
	}

	return nil
}

func (s *Service) markFailed(ctx context.Context, id uuid.UUID) {
	if err := s.subscriptionRepo.SetSyncStatus(ctx, id, string(types.SyncStatusFailed)); err != nil {
		slog.Error("webhook sync: failed to mark subscription as failed", "subscription_id", id, "error", err)
	}
}

func hashAddresses(addresses []string) string {
	sorted := append([]string(nil), addresses...)
	sort.Strings(sorted)
	joined := strings.Join(sorted, ",")
	sum := sha256.Sum256([]byte(joined))
	return fmt.Sprintf("%x", sum)
}
