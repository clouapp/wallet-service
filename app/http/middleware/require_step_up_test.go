package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/stepup"
)

type stepUpFlags struct {
	on  bool
	err error
	got uuid.UUID
}

func (f *stepUpFlags) Settings2FARequired(_ context.Context, accountID uuid.UUID) (bool, error) {
	f.got = accountID
	return f.on, f.err
}

type stepUpStore struct{ ints map[string]int64 }

func (s *stepUpStore) Put(key string, value any, _ time.Duration) error {
	s.ints[key] = value.(int64)
	return nil
}
func (s *stepUpStore) GetInt64(key string, _ ...int64) int64 { return s.ints[key] }
func (s *stepUpStore) Forget(key string) bool                { delete(s.ints, key); return true }

func TestRequire_StepUp_FollowsTheFlagTheTwoFactorStateAndTheSessionWindow(t *testing.T) {
	account, sid := uuid.New(), uuid.New()
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		flagOn   bool
		flagErr  error
		totp     bool
		grantSID uuid.UUID
		at       time.Duration // time of the request after the grant
		noAcct   bool
		status   int
		code     string
	}{
		{name: "flag off", flagOn: false},
		{name: "flag on without 2fa", flagOn: true, totp: false, status: http.StatusForbidden, code: CodeTwoFactorEnrollmentRequired},
		{name: "flag on without a window", flagOn: true, totp: true, status: http.StatusForbidden, code: CodeStepUpRequired},
		{name: "active window", flagOn: true, totp: true, grantSID: sid, at: 14 * time.Minute},
		{name: "window of another session", flagOn: true, totp: true, grantSID: uuid.New(), status: http.StatusForbidden, code: CodeStepUpRequired},
		{name: "expired window", flagOn: true, totp: true, grantSID: sid, at: stepup.Window, status: http.StatusForbidden, code: CodeStepUpRequired},
		{name: "platform route reads the global row", flagOn: true, totp: true, grantSID: sid, noAcct: true},
		{name: "flag read failure", flagErr: errors.New("down"), status: http.StatusInternalServerError, code: "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := start
			windows, err := stepup.NewService(stepup.Deps{Store: &stepUpStore{ints: map[string]int64{}}, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if tc.grantSID != uuid.Nil {
				if _, err := windows.Grant(tc.grantSID); err != nil {
					t.Fatal(err)
				}
			}
			now = start.Add(tc.at)
			flags := &stepUpFlags{on: tc.flagOn, err: tc.flagErr}

			ctx := newGuardContext(nil)
			ctx.WithValue(requestctx.KeyUser, &models.User{ID: uuid.New(), TotpEnabled: tc.totp})
			ctx.WithValue(requestctx.KeySessionID, sid)
			if !tc.noAcct {
				ctx.WithValue(requestctx.KeyAccountID, account)
			}

			RequireStepUp(flags, windows)(ctx)

			wantAccount := account
			if tc.noAcct {
				wantAccount = uuid.Nil
			}
			if flags.got != wantAccount {
				t.Fatalf("flag read for account %s, want %s", flags.got, wantAccount)
			}
			if tc.status == 0 {
				ctx.assertPassed(t)
				return
			}
			ctx.assertRefused(t, tc.status, tc.code, tc.code)
		})
	}
}
