package walletsettings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

var (
	// ErrChainNotFound is Update's refusal of a fee change on a wallet whose chain
	// record cannot be read.
	ErrChainNotFound = errors.New("chain not found")
	// ErrAlreadyArchived is Archive's refusal of a wallet that is archived.
	ErrAlreadyArchived = errors.New("wallet already archived")
)

// DefaultFreeze is how long a freeze lasts when the caller names no end.
const DefaultFreeze = 24 * time.Hour

// Wallets is the wallet persistence the settings use. *walletrecords.Wallets
// implements it.
type Wallets interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
	UpdateSettings(ctx context.Context, id uuid.UUID, columns map[string]any) error
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
	SetFrozenUntil(ctx context.Context, id uuid.UUID, until time.Time) error
}

// Chains reads a chain record. *chains.Service implements it.
type Chains interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
}

// Networks names the network a chain points at. *walletview.Service implements it.
type Networks interface {
	Network(ctx context.Context, chainID string) models.ResolvedNetwork
}

// Deps is everything the settings need. Now is time.Now when nil.
type Deps struct {
	Wallets  Wallets
	Chains   Chains
	Networks Networks
	Now      func() time.Time
}

// Service changes a wallet's settings and status.
type Service struct {
	wallets  Wallets
	chains   Chains
	networks Networks
	now      func() time.Time
}

// NewService builds the settings from Deps. It panics when a dependency is
// missing, so a mis-wired route fails at start-up.
func NewService(deps Deps) *Service {
	switch {
	case deps.Wallets == nil:
		panic("wallet settings: wallets service is required")
	case deps.Chains == nil:
		panic("wallet settings: chains service is required")
	case deps.Networks == nil:
		panic("wallet settings: wallet network reads are required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{wallets: deps.Wallets, chains: deps.Chains, networks: deps.Networks, now: now}
}

// UpdateInput is a settings update: the wallet, the raw JSON body (a field may
// be absent, null or a value) and the user who sends it.
type UpdateInput struct {
	Wallet  *models.Wallet
	Body    []byte
	ActorID uuid.UUID
}

// Update applies the body to the wallet and returns the wallet as stored. A body
// that changes nothing is ErrNoFields and a rejected field a *FieldError. The
// chain record is only read when a fee field is set, for its adapter type.
func (s *Service) Update(ctx context.Context, in UpdateInput) (*models.Wallet, error) {
	update, err := Parse(in.Body)
	if err != nil {
		return nil, err
	}
	adapterType, err := s.adapterType(ctx, in.Wallet.Chain, update)
	if err != nil {
		return nil, err
	}
	columns, err := update.Columns(in.Wallet, adapterType)
	if err != nil {
		return nil, err
	}
	if err := s.wallets.UpdateSettings(ctx, in.Wallet.ID, columns); err != nil {
		return nil, fmt.Errorf("update wallet settings: %w", err)
	}
	updated, err := s.wallets.FindByID(ctx, in.Wallet.ID)
	if err != nil {
		return nil, fmt.Errorf("reload wallet settings: %w", err)
	}
	if updated == nil {
		return nil, errors.New("reload wallet settings: wallet not returned")
	}
	audit(in.ActorID, in.Wallet, updated, columns)
	return updated, nil
}

// adapterType loads the chain only when a fee field is present. A label or
// approval change does not need a chain row.
func (s *Service) adapterType(ctx context.Context, chainID string, update Update) (string, error) {
	if !update.FeeMultiplier.Set && !update.FeeRateMin.Set && !update.FeeRateMax.Set {
		return "", nil
	}
	chain, err := s.chains.FindByID(ctx, chainID)
	if err != nil || chain == nil {
		return "", ErrChainNotFound
	}
	return chain.AdapterType, nil
}

// Freeze freezes the wallet until the given time, or for DefaultFreeze when
// until is nil, and returns the end of the freeze.
func (s *Service) Freeze(ctx context.Context, walletID uuid.UUID, until *time.Time) (time.Time, error) {
	end := s.now().Add(DefaultFreeze)
	if until != nil {
		end = *until
	}
	if err := s.wallets.SetFrozenUntil(ctx, walletID, end); err != nil {
		return time.Time{}, fmt.Errorf("freeze wallet: %w", err)
	}
	if err := s.wallets.SetStatus(ctx, walletID, models.WalletStatusFrozen); err != nil {
		return time.Time{}, fmt.Errorf("freeze wallet: %w", err)
	}
	return end, nil
}

// Archived is an archived wallet with the network its chain points at.
type Archived struct {
	Wallet  *models.Wallet
	Network models.ResolvedNetwork
}

// Archive sets the wallet's status to archived. Archiving an archived wallet is
// ErrAlreadyArchived. The wallet is returned as archived without being reloaded.
func (s *Service) Archive(ctx context.Context, wallet *models.Wallet) (Archived, error) {
	if wallet.Status == models.WalletStatusArchived {
		return Archived{}, ErrAlreadyArchived
	}
	if err := s.wallets.SetStatus(ctx, wallet.ID, models.WalletStatusArchived); err != nil {
		return Archived{}, fmt.Errorf("archive wallet: %w", err)
	}
	archived := *wallet
	archived.Status = models.WalletStatusArchived
	return Archived{Wallet: &archived, Network: s.networks.Network(ctx, wallet.Chain)}, nil
}

func audit(actorID uuid.UUID, before, after *models.Wallet, columns map[string]any) {
	changed := make([]string, 0, len(columns))
	for column := range columns {
		changed = append(changed, column)
	}
	sort.Strings(changed)
	slog.Info("wallet settings updated for wallet "+before.ID.String()+" by user "+actorID.String(),
		"chain", before.Chain,
		"fields", strings.Join(changed, ","),
		"fee_multiplier_before", nullDecimalText(before.FeeMultiplier),
		"fee_multiplier_after", nullDecimalText(after.FeeMultiplier),
		"fee_rate_min_before", intPointerText(before.FeeRateMin),
		"fee_rate_min_after", intPointerText(after.FeeRateMin),
		"fee_rate_max_before", intPointerText(before.FeeRateMax),
		"fee_rate_max_after", intPointerText(after.FeeRateMax),
	)
}

func nullDecimalText(value numeric.NullDecimal) string {
	if !value.Valid {
		return "null"
	}
	return value.Decimal.String()
}

func intPointerText(value *int) string {
	if value == nil {
		return "null"
	}
	return strconv.Itoa(*value)
}
