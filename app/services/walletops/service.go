// Package walletops composes the wallet and address operations both HTTP
// surfaces serve out of the wallet domain service, the deposit address cache
// and the chain catalogue, so a controller makes one call per route.
package walletops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// ErrNoFields is UpdateAddress's refusal of an update that changes nothing.
var ErrNoFields = errors.New("no fields to update")

// ErrAddressesUnavailable wraps a failed read of a wallet's addresses. The
// cause stays on the error chain.
var ErrAddressesUnavailable = errors.New("addresses unavailable")

// Wallets is the wallet domain service the operations call. *wallet.Service
// implements it.
type Wallets interface {
	CreateWallet(ctx context.Context, accountID uuid.UUID, chainID, label, passphrase string) (*wallet.CreateWalletResult, error)
	ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) (*models.Wallet, error)
	GetWallet(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
	GenerateAddress(ctx context.Context, walletID uuid.UUID, externalUserID, label, metadata, passphrase string) (*models.Address, error)
	UpdateAddress(ctx context.Context, addressID uuid.UUID, fields map[string]interface{}) (*models.Address, error)
	LookupAddressForAccount(ctx context.Context, chainID, address string, accountID uuid.UUID) (*models.Address, error)
	ListUserAddressesForAccount(ctx context.Context, externalUserID string, accountID uuid.UUID) ([]models.Address, error)
}

// AddressCache reloads the monitored addresses of a chain. *deposit.Service
// implements it.
type AddressCache interface {
	RefreshAddressCache(ctx context.Context, chainID string) error
}

// ChainRegistry lists the chains the service runs. *chain.Registry implements it.
type ChainRegistry interface {
	ChainIDs() []string
}

// Deps is everything the operations need. Every field is required.
type Deps struct {
	Wallets   Wallets
	Addresses *walletrecords.Addresses
	Chains    *chainsvc.Service
	Cache     AddressCache
	Registry  ChainRegistry
}

// Service runs the wallet and address operations.
type Service struct {
	wallets   Wallets
	addresses *walletrecords.Addresses
	chains    *chainsvc.Service
	cache     AddressCache
	registry  ChainRegistry
}

// NewService builds the operations from Deps. It panics when a dependency is
// missing, so a mis-wired route fails at start-up.
func NewService(deps Deps) *Service {
	switch {
	case deps.Wallets == nil:
		panic("wallet operations: wallet service is required")
	case deps.Addresses == nil:
		panic("wallet operations: addresses service is required")
	case deps.Chains == nil:
		panic("wallet operations: chains service is required")
	case deps.Cache == nil:
		panic("wallet operations: address cache is required")
	case deps.Registry == nil:
		panic("wallet operations: chain registry is required")
	}
	return &Service{
		wallets:   deps.Wallets,
		addresses: deps.Addresses,
		chains:    deps.Chains,
		cache:     deps.Cache,
		registry:  deps.Registry,
	}
}

// CreateWallet creates a wallet for the account on any chain the service runs.
func (s *Service) CreateWallet(ctx context.Context, accountID uuid.UUID, chainID, label, passphrase string) (*wallet.CreateWalletResult, error) {
	return s.wallets.CreateWallet(ctx, accountID, chainID, label, passphrase)
}

// ActivateWallet confirms the user saved the wallet's KeyCard, by its activation code.
func (s *Service) ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) error {
	_, err := s.wallets.ActivateWallet(ctx, walletID, code)
	return err
}

// CreateWalletInput is a wallet creation for an account whose environment
// limits the chains it may use.
type CreateWalletInput struct {
	AccountID   uuid.UUID
	Environment string
	Chain       string
	Label       string
	Passphrase  string
}

// CreateWalletInEnvironment creates a wallet on a chain the account environment
// may use. A chain of the other network kind is chainsvc.ErrChainNotInEnvironment.
// A chain the catalogue cannot read, or does not hold, is not a refusal here:
// the wallet service answers for the chain.
func (s *Service) CreateWalletInEnvironment(ctx context.Context, in CreateWalletInput) (*wallet.CreateWalletResult, error) {
	if _, err := s.chains.FindForEnvironment(ctx, in.Chain, in.Environment); err != nil {
		if errors.Is(err, chainsvc.ErrChainNotInEnvironment) {
			return nil, err
		}
		if !errors.Is(err, models.ErrRepositoryNotFound) {
			slog.Warn("create wallet: check chain environment", "chain", in.Chain, "error", err)
		}
	}
	return s.wallets.CreateWallet(ctx, in.AccountID, in.Chain, in.Label, in.Passphrase)
}

// GenerateAddressInput is a deposit address request for one wallet.
type GenerateAddressInput struct {
	WalletID       uuid.UUID
	ExternalUserID string
	Label          string
	Metadata       string
	Passphrase     string
}

// GenerateAddress derives a deposit address and reloads the chain's address
// cache. A cache that cannot be refreshed is logged, not an error: the address
// exists and the scanner falls back to the database.
func (s *Service) GenerateAddress(ctx context.Context, in GenerateAddressInput) (*models.Address, error) {
	address, err := s.wallets.GenerateAddress(ctx, in.WalletID, in.ExternalUserID, in.Label, in.Metadata, in.Passphrase)
	if err != nil {
		return nil, err
	}

	owner, err := s.wallets.GetWallet(ctx, in.WalletID)
	if err != nil {
		slog.Warn("generate address: load wallet to refresh the address cache", "wallet", in.WalletID, "error", err)
		return address, nil
	}
	if err := s.cache.RefreshAddressCache(ctx, owner.Chain); err != nil {
		slog.Warn("generate address: refresh the address cache", "chain", owner.Chain, "error", err)
	}
	return address, nil
}

// UpdateAddressInput changes the fields that are set; a nil field is left alone.
type UpdateAddressInput struct {
	AddressID      uuid.UUID
	Label          *string
	ExternalUserID *string
}

// UpdateAddress changes an address's label and external user id. An update that
// sets neither is ErrNoFields.
func (s *Service) UpdateAddress(ctx context.Context, in UpdateAddressInput) (*models.Address, error) {
	fields := make(map[string]interface{}, 2)
	if in.Label != nil {
		fields["label"] = *in.Label
	}
	if in.ExternalUserID != nil {
		fields["external_user_id"] = *in.ExternalUserID
	}
	if len(fields) == 0 {
		return nil, ErrNoFields
	}
	return s.wallets.UpdateAddress(ctx, in.AddressID, fields)
}

// ListAddresses returns a page of a wallet's addresses with their total.
func (s *Service) ListAddresses(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	addresses, total, err := s.addresses.PaginateByWalletID(ctx, walletID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrAddressesUnavailable, err)
	}
	return addresses, total, nil
}

// ErrAddressNotFound is LookupAddress's answer for an address the account does
// not own, which is also an address that does not exist.
var ErrAddressNotFound = errors.New("address not found")

// LookupAddress finds a deposit address by its on-chain string among the
// account's wallets. With a chain it asks that chain; without one it tries every
// registered chain. Any failure to find it, on a chain or on all of them, is
// ErrAddressNotFound, so an address of another account is not told apart from
// one that does not exist.
func (s *Service) LookupAddress(ctx context.Context, accountID uuid.UUID, address, chainID string) (*models.Address, error) {
	if chainID != "" {
		found, err := s.wallets.LookupAddressForAccount(ctx, chainID, address, accountID)
		if err != nil || found == nil {
			return nil, ErrAddressNotFound
		}
		return found, nil
	}

	for _, id := range s.registry.ChainIDs() {
		found, err := s.wallets.LookupAddressForAccount(ctx, id, address, accountID)
		if err == nil && found != nil {
			return found, nil
		}
	}
	return nil, ErrAddressNotFound
}

// UserAddresses returns the account's addresses assigned to an external user
// id, across all chains. An empty list is the answer both for an id that does
// not exist and for one that belongs to another account.
func (s *Service) UserAddresses(ctx context.Context, accountID uuid.UUID, externalUserID string) ([]models.Address, error) {
	return s.wallets.ListUserAddressesForAccount(ctx, externalUserID, accountID)
}
