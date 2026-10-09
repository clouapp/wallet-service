// Package walletview reads wallets the way the HTTP surfaces show them: a page
// or one wallet with its network, and the balances of its configured assets.
// Testnet wallets carry no USD value.
package walletview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

var (
	// ErrAccountRequired is the answer when the caller's account is not known.
	ErrAccountRequired = errors.New("account is required")
	// ErrViewerRequired is the answer when a caller who sees only the wallets
	// they belong to is not identified.
	ErrViewerRequired = errors.New("unauthorized")
	// ErrWalletNotFound is the answer for a wallet the account does not own and for
	// one the caller may not see, so the two are not told apart.
	ErrWalletNotFound = errors.New("wallet not found")
)

// FetchError is a failed read. What names the data for the client.
type FetchError struct {
	What string
	Err  error
}

func (e *FetchError) Error() string { return fmt.Sprintf("fetch %s: %v", e.What, e.Err) }
func (e *FetchError) Unwrap() error { return e.Err }

func fetchFailed(what string, err error) error { return &FetchError{What: what, Err: err} }

// Viewer is who asks. The zero value sees every wallet of the account.
type Viewer struct {
	// MemberOnly limits the wallets to those the user is a member of.
	MemberOnly bool
	UserID     uuid.UUID
	// AccountMissing marks a caller whose account the request did not carry.
	AccountMissing bool
}

// Deps is everything the reads need. Every field is required.
type Deps struct {
	Wallets  *walletrecords.Wallets
	Members  *walletrecords.Members
	Balances *walletrecords.Balances
	// Transactions is required by TransactionsOf and Transaction.
	Transactions *walletrecords.Transactions
	Chains       *chainsvc.Service
	// Cipher opens the chain RPC URLs. It is read on each use, so the encryption
	// key may be bound after the service is built.
	Cipher func() settings.Cipher
}

// Service reads wallets for the HTTP surfaces.
type Service struct {
	wallets  *walletrecords.Wallets
	members  *walletrecords.Members
	balances *walletrecords.Balances
	txs      *walletrecords.Transactions
	chains   *chainsvc.Service
	cipher   func() settings.Cipher
}

// NewService builds the reads from Deps. It panics when a dependency is
// missing, so a mis-wired route fails at start-up.
func NewService(deps Deps) *Service {
	switch {
	case deps.Wallets == nil:
		panic("wallet view: wallets service is required")
	case deps.Members == nil:
		panic("wallet view: wallet members service is required")
	case deps.Balances == nil:
		panic("wallet view: balances service is required")
	case deps.Chains == nil:
		panic("wallet view: chains service is required")
	case deps.Cipher == nil:
		panic("wallet view: cipher is required")
	}
	return &Service{
		wallets:  deps.Wallets,
		members:  deps.Members,
		balances: deps.Balances,
		txs:      deps.Transactions,
		chains:   deps.Chains,
		cipher:   deps.Cipher,
	}
}

// Item is a wallet of a page: its network and the native and configured token
// balances of its last refresh. On a testnet the wallet and its balances carry
// no USD value.
type Item struct {
	Wallet  models.Wallet
	Network models.ResolvedNetwork
	Assets  []models.WalletAssetBalance
}

// Page is a page of items with the total of the filter.
type Page struct {
	Items []Item
	Total int64
}

// ListInput is a page of the wallets an account holds, optionally of one chain.
type ListInput struct {
	AccountID uuid.UUID
	Chain     string
	Limit     int
	Offset    int
	Viewer    Viewer
}

// List returns a page of wallets with their networks and balances, reading each
// chain and its tokens once.
func (s *Service) List(ctx context.Context, in ListInput) (Page, error) {
	if err := checkViewer(in.Viewer); err != nil {
		return Page{}, err
	}

	var (
		wallets []models.Wallet
		total   int64
		err     error
	)
	if in.Viewer.MemberOnly {
		wallets, total, err = s.wallets.PaginateByAccountAndMember(ctx, in.AccountID, in.Viewer.UserID, in.Chain, in.Limit, in.Offset)
	} else {
		wallets, total, err = s.wallets.PaginateByAccount(ctx, in.AccountID, in.Chain, in.Limit, in.Offset)
	}
	if err != nil {
		slog.Error("list wallets", "account", in.AccountID, "error", err)
		return Page{}, fetchFailed("wallets", err)
	}

	items, err := s.items(ctx, wallets)
	if err != nil {
		slog.Error("load wallet list balances", "account", in.AccountID, "error", err)
		return Page{}, fetchFailed("wallet balances", err)
	}
	return Page{Items: items, Total: total}, nil
}

// checkViewer refuses a caller who cannot be placed: the account must be known,
// and a member-only caller must be identified.
func checkViewer(viewer Viewer) error {
	if viewer.AccountMissing {
		return ErrAccountRequired
	}
	if viewer.MemberOnly && viewer.UserID == uuid.Nil {
		return ErrViewerRequired
	}
	return nil
}

// GetInput is one wallet of an account.
type GetInput struct {
	AccountID uuid.UUID
	WalletID  uuid.UUID
	Viewer    Viewer
}

// Detail is a wallet with the network its chain points at.
type Detail struct {
	Wallet  *models.Wallet
	Network models.ResolvedNetwork
}

// Get returns a wallet of the account. A wallet the account does not hold is
// ErrWalletNotFound, and so is one a member-only caller does not belong to.
func (s *Service) Get(ctx context.Context, in GetInput) (Detail, error) {
	wallet, err := s.wallets.FindByIDAndAccount(ctx, in.WalletID, in.AccountID)
	if err != nil || wallet == nil {
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			slog.Error("find wallet", "wallet", in.WalletID, "error", err)
		}
		return Detail{}, ErrWalletNotFound
	}
	if err := s.requireVisible(ctx, wallet.ID, in.Viewer); err != nil {
		return Detail{}, err
	}
	return Detail{Wallet: wallet, Network: s.Network(ctx, wallet.Chain)}, nil
}

// requireVisible refuses a wallet a member-only caller does not belong to.
func (s *Service) requireVisible(ctx context.Context, walletID uuid.UUID, viewer Viewer) error {
	if err := checkViewer(viewer); err != nil {
		return err
	}
	if !viewer.MemberOnly {
		return nil
	}
	_, err := s.members.FindByWalletAndUser(ctx, walletID, viewer.UserID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, models.ErrRepositoryNotFound):
		return ErrWalletNotFound
	}
	return fetchFailed("wallet", err)
}

// Balances returns the balances of a wallet's configured assets: the native
// balance and the tokens the chain still configures, as of the last refresh.
// A testnet wallet carries no USD price or value.
func (s *Service) Balances(ctx context.Context, wallet *models.Wallet) ([]models.WalletAssetBalance, error) {
	rows, err := s.balances.ListByWallet(ctx, wallet.ID)
	if err != nil {
		return nil, fetchFailed("balances", err)
	}
	tokens, err := s.chains.FindTokens(ctx, wallet.Chain)
	if err != nil {
		return nil, fetchFailed("chain tokens", err)
	}
	return unpricedOnTestnet(configuredAssetBalances(rows, tokens), s.Network(ctx, wallet.Chain)), nil
}
