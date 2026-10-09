package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/features"
)

// TestFeature_Enabled_PausesTheRouteOnTheWalletAccountFlag drives the money
// movement gate the withdrawal and consolidate routes mount last: an open
// flag lets the request through untouched, a paused account flag or an
// explicit global false is 409 with the pause code as code and message, and
// a flag read that fails is the generic 500. The account gated on is the
// wallet's own, else the caller's.
func TestFeature_Enabled_PausesTheRouteOnTheWalletAccountFlag(t *testing.T) {
	walletAccount, callerAccount := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		store   *flagStore
		wallet  *models.Wallet
		status  int
		code    string
		message string
	}{
		{name: "open", store: newFlagStore(), wallet: &models.Wallet{AccountID: &walletAccount}},
		{
			name:   "paused wallet account",
			store:  newFlagStore().account(walletAccount, false),
			wallet: &models.Wallet{AccountID: &walletAccount},
			status: http.StatusConflict, code: features.CodeWithdrawalsPaused, message: features.CodeWithdrawalsPaused,
		},
		{
			name:  "a paused caller account does not pause a wallet of another account",
			store: newFlagStore().account(callerAccount, false), wallet: &models.Wallet{AccountID: &walletAccount},
		},
		{
			name:   "no wallet gates on the caller account",
			store:  newFlagStore().account(callerAccount, false),
			status: http.StatusConflict, code: features.CodeWithdrawalsPaused, message: features.CodeWithdrawalsPaused,
		},
		{
			name:   "explicit global false",
			store:  newFlagStore().global(false).account(walletAccount, true),
			wallet: &models.Wallet{AccountID: &walletAccount},
			status: http.StatusConflict, code: features.CodeWithdrawalsPaused, message: features.CodeWithdrawalsPaused,
		},
		{
			name:   "flag read failure",
			store:  newFlagStore().failing(),
			wallet: &models.Wallet{AccountID: &walletAccount},
			status: http.StatusInternalServerError, code: resources.CodeInternalError, message: "internal_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := features.NewService(features.Deps{Store: tc.store, Admins: noPlatformAdmins{}, Activity: discardActivity{}})
			ctx := newGuardContext(nil)
			ctx.WithValue(requestctx.KeyAccountID, callerAccount)
			if tc.wallet != nil {
				ctx.WithValue(requestctx.KeyWallet, tc.wallet)
			}

			FeatureEnabled(flags, features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused, "create_wallet_withdrawal")(ctx)

			if tc.status == 0 {
				ctx.assertPassed(t)
				return
			}
			ctx.assertRefused(t, tc.status, tc.code, tc.message)
		})
	}
}

func TestFeature_Enabled_RefusesToBuildWithoutFlags(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("FeatureEnabled built a gate without flags")
		}
	}()
	FeatureEnabled(nil, features.FlagSweepEnabled, features.CodeSweepPaused, "consolidate")
}

// flagStore is the features.Store of one flag: the global row and the account
// rows of withdrawals-enabled, or a read that fails.
type flagStore struct {
	rows      map[uuid.UUID]bool
	globalRow *bool
	err       error
}

func newFlagStore() *flagStore { return &flagStore{rows: map[uuid.UUID]bool{}} }

func (s *flagStore) account(id uuid.UUID, enabled bool) *flagStore {
	s.rows[id] = enabled
	return s
}

func (s *flagStore) global(enabled bool) *flagStore {
	s.globalRow = &enabled
	return s
}

func (s *flagStore) failing() *flagStore {
	s.err = errors.New("pq: connection refused")
	return s
}

func (s *flagStore) ListAccount(_ context.Context, accountID uuid.UUID) ([]models.Feature, error) {
	if s.err != nil {
		return nil, s.err
	}
	enabled, ok := s.rows[accountID]
	if !ok {
		return nil, nil
	}
	return []models.Feature{{AccountID: accountID, Key: features.FlagWithdrawalsEnabled, Enabled: enabled}}, nil
}

func (s *flagStore) GetGlobal(context.Context, string) (bool, bool, error) {
	if s.err != nil {
		return false, false, s.err
	}
	if s.globalRow == nil {
		return false, false, nil
	}
	return *s.globalRow, true, nil
}

func (s *flagStore) Upsert(context.Context, uuid.UUID, string, bool) error {
	return errors.New("read only")
}

func (s *flagStore) ListGlobal(context.Context) ([]models.GlobalFeature, error) {
	return nil, errors.New("not used")
}

func (s *flagStore) UpsertGlobal(context.Context, string, bool) error {
	return errors.New("read only")
}

type noPlatformAdmins struct{}

func (noPlatformAdmins) Contains(context.Context, uuid.UUID) (bool, error) { return false, nil }

type discardActivity struct{}

func (discardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (discardActivity) Append(context.Context, models.AccountActivity) error { return nil }
