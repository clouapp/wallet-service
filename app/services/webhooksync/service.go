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
	"github.com/macrowallets/waas/pkg/types"
)

const sealedProviderKeyPrefix = "enc:v1:"

// ProviderKey returns the credential for one provider on this sync.
// Implementations open sealed settings on each call. An empty result fails
// the sync. The key must not be logged.
type ProviderKey func(ctx context.Context, provider string) string

// activeAddresses is the address list a sync pushes to the provider.
type activeAddresses interface {
	PluckActiveAddresses(ctx context.Context, chainID string) ([]string, error)
}

// signingSecretOpener opens the subscription signing secret. The default
// uses the process cipher. A test supplies its own so the suite does not
// boot the cipher. The plaintext is not logged.
type signingSecretOpener func(sealed string) (string, error)

// subscriptionStore is the provider-subscription persistence this service uses.
type subscriptionStore interface {
	FindByChainID(ctx context.Context, chainID string) (*models.WebhookSubscription, error)
	FindAllActive(ctx context.Context) ([]models.WebhookSubscription, error)
	SetSyncStatus(ctx context.Context, id uuid.UUID, status string) error
	RecordSync(ctx context.Context, id uuid.UUID, status, hash string, syncedAt time.Time) error
}

// AddressSyncer pushes the full active address list to one provider webhook.
type AddressSyncer interface {
	SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error
}

type Service struct {
	subscriptionRepo subscriptionStore
	addressRepo      activeAddresses
	providers        map[string]AddressSyncer
	providerKey      ProviderKey
	openSecret       signingSecretOpener
	mu               sync.Map // subscription id -> *sync.Mutex
}

// Deps is everything the webhook sync service needs. A nil field means that
// dependency is absent.
type Deps struct {
	Subscriptions subscriptionStore
	Addresses     activeAddresses
	Providers     map[string]AddressSyncer
	// ProviderKey re-reads the provider credential on every sync. Nil keeps
	// the providers in Providers and does not consult settings.
	ProviderKey ProviderKey
}

// NewService wires the webhook sync service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		subscriptionRepo: deps.Subscriptions,
		addressRepo:      deps.Addresses,
		providers:        deps.Providers,
		providerKey:      deps.ProviderKey,
	}
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

	if s.providerKey != nil {
		resolved := strings.TrimSpace(s.providerKey(ctx, providerName))
		if resolved == "" || strings.HasPrefix(resolved, sealedProviderKeyPrefix) {
			slog.Error("webhook sync: provider key unavailable", "provider", providerName)
			s.markFailed(ctx, sub.ID)
			return fmt.Errorf("webhook provider key is empty")
		}
	}

	if _, err := s.decryptSigningSecret(sub.SigningSecret); err != nil {
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

func (s *Service) decryptSigningSecret(sealed string) (string, error) {
	if s != nil && s.openSecret != nil {
		return s.openSecret(sealed)
	}
	return facades.Crypt().DecryptString(sealed)
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
