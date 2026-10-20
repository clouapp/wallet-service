// Package balanceaudit compares, for the deposit addresses of one chain, the
// balance the chain holds with the balance the recorded transactions explain:
// credited deposits, sweeps and gas seeds in, withdrawals, sweeps and gas seeds out,
// and the native fees the outbound ones paid. It only reads: nothing is written,
// signed or broadcast, and the chain is read a bounded number of times, spaced.
package balanceaudit

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// ErrInvalidAudit is an audit request that cannot run as asked.
var ErrInvalidAudit = errors.New("balanceaudit: invalid audit")

// chainReader is the chain registry: the adapter of a chain and the tokens it lists.
type chainReader interface {
	Chain(chainID string) (types.Chain, error)
	TokensForChain(chainID string) []types.Token
	FindTokenByContract(chainID, contract string) (*types.Token, error)
}

// addressLister pages the addresses of a chain by id (the address repository).
type addressLister interface {
	ListForBalanceAudit(ctx context.Context, chainID string, accountID *uuid.UUID, afterID uuid.UUID, limit int) ([]models.Address, error)
}

// ledgerReader sums what the recorded transactions moved in and out of addresses
// (the transaction repository).
type ledgerReader interface {
	AddressLedger(ctx context.Context, chainID string, addresses []string) ([]models.AddressLedgerEntry, error)
	AddressFees(ctx context.Context, chainID string, addresses []string, withAccountRent bool) ([]models.AddressFeeEntry, error)
}

// Service runs balance audits.
type Service struct {
	chains    chainReader
	addresses addressLister
	ledger    ledgerReader
}

// Deps is everything the service needs; every field is required.
type Deps struct {
	Chains    chainReader
	Addresses addressLister
	Ledger    ledgerReader
}

// NewService wires the audit; it panics on a missing dependency (composition root only).
func NewService(deps Deps) *Service {
	if deps.Chains == nil || deps.Addresses == nil || deps.Ledger == nil {
		panic("balanceaudit: chains, addresses and ledger are required")
	}
	return &Service{chains: deps.Chains, addresses: deps.Addresses, ledger: deps.Ledger}
}
