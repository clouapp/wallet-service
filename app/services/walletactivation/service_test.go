package walletactivation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/stretchr/testify/require"
)

type activationFixture struct {
	mu            sync.Mutex
	wallet        models.Wallet
	account       models.Account
	chain         models.Chain
	calls         int
	activationErr error
	foreignResult bool
	locks         []string
}
type activationWallets struct{ f *activationFixture }

func (s activationWallets) Within(ctx context.Context, fn func(context.Context) error) error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	before := s.f.wallet
	err := fn(ctx)
	if err != nil {
		s.f.wallet = before
	}
	return err
}
func (s activationWallets) LockByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	s.f.locks = append(s.f.locks, "wallet")
	w := s.f.wallet
	return &w, nil
}

type activationAccounts struct{ f *activationFixture }

func (s activationAccounts) LockByID(context.Context, uuid.UUID) (*models.Account, error) {
	s.f.locks = append(s.f.locks, "account")
	a := s.f.account
	return &a, nil
}

type activationChains struct{ f *activationFixture }

func (s activationChains) LockByID(context.Context, string) (*models.Chain, error) {
	s.f.locks = append(s.f.locks, "chain")
	c := s.f.chain
	return &c, nil
}

type activationDomain struct{ f *activationFixture }

func (s activationDomain) ActivateWallet(_ context.Context, id uuid.UUID, code string) (*models.Wallet, error) {
	s.f.calls++
	if id != s.f.wallet.ID || s.f.wallet.ActivationCode == nil || code != *s.f.wallet.ActivationCode {
		return nil, errors.New("incorrect private credential")
	}
	s.f.wallet.Status = "active"
	s.f.wallet.ActivationCode = nil
	if s.f.activationErr != nil {
		return nil, s.f.activationErr
	}
	w := s.f.wallet
	if s.f.foreignResult {
		w.ID = uuid.New()
	}
	return &w, nil
}
func newActivationFixture() (*activationFixture, *Service, Input) {
	accountID, walletID := uuid.New(), uuid.New()
	code := "038149"
	f := &activationFixture{wallet: models.Wallet{ID: walletID, AccountID: &accountID, Chain: "arbitrum", Status: "pending", ActivationCode: &code, MPCCustomerShare: "private-share-not-in-report"},
		account: models.Account{ID: accountID, Status: models.StatusActive, Environment: models.EnvironmentTest}, chain: models.Chain{ID: "arbitrum", Status: models.StatusActive, IsTestnet: true}}
	service := NewService(Deps{Wallets: activationWallets{f}, Accounts: activationAccounts{f}, Chains: activationChains{f}, Activate: activationDomain{f}})
	return f, service, Input{AccountID: accountID.String(), WalletID: walletID.String(), ExpectedChain: "arbitrum"}
}

func TestRecover_DryRunUsesExactLocksWithoutActivationOrSecretOutput(t *testing.T) {
	f, service, input := newActivationFixture()
	report, err := service.Recover(context.Background(), input)
	require.NoError(t, err)
	require.False(t, report.Applied)
	require.Equal(t, "pending", report.Status)
	require.Zero(t, f.calls)
	require.Equal(t, []string{"chain", "account", "wallet"}, f.locks)
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), *f.wallet.ActivationCode)
	require.NotContains(t, string(encoded), f.wallet.MPCCustomerShare)
}

func TestRecover_ExplicitApplyUsesOfficialActivationAndRefusesARepeat(t *testing.T) {
	f, service, input := newActivationFixture()
	input.Apply = true
	report, err := service.Recover(context.Background(), input)
	require.NoError(t, err)
	require.True(t, report.Applied)
	require.Equal(t, "active", f.wallet.Status)
	require.Nil(t, f.wallet.ActivationCode)
	_, err = service.Recover(context.Background(), input)
	require.ErrorIs(t, err, ErrBinding)
	require.Equal(t, 1, f.calls)
}

func TestRecover_RefusesUnsafeScopeWithoutCallingActivation(t *testing.T) {
	for _, kind := range []string{"foreign account", "foreign wallet", "foreign wallet owner", "wrong chain", "production account", "mainnet chain", "inactive account", "inactive chain", "frozen wallet", "freeze metadata", "already active", "missing code", "malformed code", "empty selection"} {
		t.Run(kind, func(t *testing.T) {
			f, service, input := newActivationFixture()
			input.Apply = true
			switch kind {
			case "foreign account":
				f.account.ID = uuid.New()
			case "foreign wallet":
				f.wallet.ID = uuid.New()
			case "foreign wallet owner":
				id := uuid.New()
				f.wallet.AccountID = &id
			case "wrong chain":
				f.wallet.Chain = "eth"
			case "production account":
				f.account.Environment = models.EnvironmentProd
			case "mainnet chain":
				f.chain.IsTestnet = false
			case "inactive account":
				f.account.Status = "frozen"
			case "inactive chain":
				f.chain.Status = "disabled"
			case "frozen wallet":
				f.wallet.Status = "frozen"
			case "freeze metadata":
				at := time.Now().Add(time.Hour)
				f.wallet.FrozenUntil = &at
			case "already active":
				f.wallet.Status = "active"
			case "missing code":
				f.wallet.ActivationCode = nil
			case "malformed code":
				value := "bad-code"
				f.wallet.ActivationCode = &value
			case "empty selection":
				input.AccountID = ""
			}
			_, err := service.Recover(context.Background(), input)
			require.Error(t, err)
			require.Zero(t, f.calls)
		})
	}
}

func TestRecover_UnknownActivationFailureRollsBackAndNeverEchoesCredential(t *testing.T) {
	f, service, input := newActivationFixture()
	input.Apply = true
	code := *f.wallet.ActivationCode
	f.activationErr = errors.New("synthetic driver error containing " + code)
	_, err := service.Recover(context.Background(), input)
	require.ErrorIs(t, err, ErrActivation)
	require.False(t, strings.Contains(err.Error(), code))
	require.Equal(t, "pending", f.wallet.Status)
	f.activationErr = nil
	f.foreignResult = true
	_, err = service.Recover(context.Background(), input)
	require.ErrorIs(t, err, ErrActivation)
	require.Equal(t, "pending", f.wallet.Status)
}

func TestRecover_ConcurrentApplyHasOneWinner(t *testing.T) {
	f, service, input := newActivationFixture()
	input.Apply = true
	start := make(chan struct{})
	results := make(chan error, 16)
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.Recover(context.Background(), input)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrBinding)
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, f.calls)
}
