// Package withdrawrecoveryflow scopes the recovery API to its authenticated
// wallet and obtains immutable economic facts from the server-side observer.
package withdrawrecoveryflow

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
)

type BindingReader interface {
	ReadBinding(context.Context, uuid.UUID, uuid.UUID) (recovery.Binding, error)
}

type Scope struct {
	WalletID   uuid.UUID
	AccountID  uuid.UUID
	Service    *recovery.Service
	Runtime    BindingReader
	Store      recovery.Store
	GuardWrite func(context.Context) error
	// QuoteGas only reads a bounded preparation quote. A nil result means no
	// native preparation is needed; it never signs, claims or transmits.
	QuoteGas func(context.Context, recovery.Binding) (*recovery.GasSeedPlan, error)
}

type Factory func(context.Context, uuid.UUID) (*Scope, error)
type Flow struct{ factory Factory }

func New(factory Factory) *Flow {
	if factory == nil {
		panic("withdrawal recovery flow: scoped factory is required")
	}
	return &Flow{factory: factory}
}

func (f *Flow) scope(ctx context.Context, walletID uuid.UUID) (*Scope, error) {
	if ctx == nil || walletID == uuid.Nil {
		return nil, recovery.ErrNotFound
	}
	s, err := f.factory(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if s == nil || s.WalletID != walletID || s.AccountID == uuid.Nil || s.Service == nil || s.Runtime == nil || s.Store == nil {
		return nil, recovery.ErrNotFound
	}
	return s, nil
}

func (f *Flow) Inspect(ctx context.Context, walletID, operationID, originalID uuid.UUID, fees recovery.FeeEnvelope, seed *recovery.GasSeedPlan, preparedBy string) (*recovery.Proposal, error) {
	s, err := f.scope(ctx, walletID)
	if err != nil {
		return nil, err
	}
	b, err := s.Runtime.ReadBinding(ctx, operationID, originalID)
	if err != nil {
		return nil, err
	}
	if b.WalletID != walletID || b.AccountID != s.AccountID || b.OperationID != operationID || b.OriginalTransactionID != originalID {
		return nil, recovery.ErrNotFound
	}
	if s.QuoteGas != nil {
		quote, err := s.QuoteGas(ctx, b)
		if err != nil {
			return nil, err
		}
		if seed != nil {
			givenHash, _ := recovery.Hash(seed)
			quoteHash, _ := recovery.Hash(quote)
			if givenHash != quoteHash {
				return nil, recovery.ErrRefused
			}
		}
		seed = quote
	}
	return s.Service.Inspect(ctx, b, fees, seed, preparedBy)
}

func (f *Flow) Apply(ctx context.Context, walletID, operationID uuid.UUID, p recovery.Proposal, review recovery.Review, operator, passphrase string) (*recovery.Result, error) {
	s, err := f.scope(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if p.Binding.WalletID != walletID || p.Binding.AccountID != s.AccountID || p.Binding.OperationID != operationID {
		return nil, recovery.ErrNotFound
	}
	if s.GuardWrite != nil {
		if err := s.GuardWrite(ctx); err != nil {
			return nil, err
		}
	}
	return s.Service.Apply(ctx, p, review, operator, passphrase)
}

func scopedAttempt(ctx context.Context, s *Scope, walletID, operationID uuid.UUID) error {
	row, err := s.Store.FindByOperation(ctx, operationID)
	if errors.Is(err, models.ErrRepositoryNotFound) || errors.Is(err, recovery.ErrNotFound) {
		return recovery.ErrNotFound
	}
	if err != nil {
		return err
	}
	if row == nil || row.OperationID != operationID || row.WalletID != walletID || row.AccountID != s.AccountID {
		return recovery.ErrNotFound
	}
	return nil
}

func (f *Flow) Resume(ctx context.Context, walletID, operationID uuid.UUID, operator, passphrase string) (*recovery.Result, error) {
	s, err := f.scope(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if err := scopedAttempt(ctx, s, walletID, operationID); err != nil {
		return nil, err
	}
	if s.GuardWrite != nil {
		if err := s.GuardWrite(ctx); err != nil {
			return nil, err
		}
	}
	return s.Service.Resume(ctx, operationID, operator, passphrase)
}

func (f *Flow) Lookup(ctx context.Context, walletID, operationID uuid.UUID) (*recovery.Result, error) {
	s, err := f.scope(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if err := scopedAttempt(ctx, s, walletID, operationID); err != nil {
		return nil, err
	}
	return s.Service.Lookup(ctx, operationID)
}
