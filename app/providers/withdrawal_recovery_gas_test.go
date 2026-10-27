package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/sweep"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	runtime "github.com/macrowallets/waas/app/services/withdrawrecoveryruntime"
)

type gasPreparerStub struct {
	quote                               *sweep.DirectChildGasPlan
	observed                            *sweep.DirectChildGasResult
	observeErr                          error
	prepared                            *sweep.DirectChildGasPlan
	bound                               sweep.DirectChildGasBinding
	quotes, prepares, observes, resumes int
	beforeObserve                       func()
}

func (g *gasPreparerStub) QuoteDirectChildGas(_ context.Context, b sweep.DirectChildGasBinding) (*sweep.DirectChildGasPlan, error) {
	g.quotes++
	g.bound = b
	return g.quote, nil
}
func (g *gasPreparerStub) PrepareDirectChildGas(_ context.Context, b sweep.DirectChildGasBinding, p sweep.DirectChildGasPlan, _ string) (*sweep.DirectChildGasResult, error) {
	g.prepares++
	g.bound = b
	g.prepared = &p
	return g.observed, nil
}
func (g *gasPreparerStub) ObserveDirectChildGas(_ context.Context, b sweep.DirectChildGasBinding) (*sweep.DirectChildGasResult, error) {
	g.observes++
	if g.beforeObserve != nil {
		g.beforeObserve()
	}
	g.bound = b
	return g.observed, g.observeErr
}
func (g *gasPreparerStub) ResumeDirectChildGas(_ context.Context, b sweep.DirectChildGasBinding) (*sweep.DirectChildGasResult, error) {
	g.resumes++
	g.bound = b
	return g.observed, nil
}

type gasObserverStub struct{ observation recovery.Observation }

func (o gasObserverStub) Observe(context.Context, recovery.Binding, string) (*recovery.Observation, error) {
	value := o.observation
	return &value, nil
}

type gasRPCStub struct {
	network, latest, pending uint64
	calls                    int
}

func (r *gasRPCStub) Call(_ context.Context, method string, out interface{}, params ...interface{}) error {
	r.calls++
	value := r.network
	if method == "eth_getTransactionCount" {
		value = r.latest
		if params[1] == "pending" {
			value = r.pending
		}
	}
	b, _ := json.Marshal(fmt.Sprintf("0x%x", value))
	return json.Unmarshal(b, out)
}
func gasBridgeFixture(t *testing.T) (*recoveryGasBridge, *gasPreparerStub, *models.WithdrawalRecoveryAttempt, recovery.Binding, recovery.GasSeedPlan) {
	t.Helper()
	nonce := uint64(0)
	b := recovery.Binding{OperationID: uuid.New(), OriginalTransactionID: uuid.New(), AccountID: uuid.New(), WalletID: uuid.New(), SourceAddressID: uuid.New(), SourceAddress: "0x" + strings.Repeat("1", 40), NetworkID: 421614, Nonce: &nonce, Asset: "USDC", TokenContract: "0x" + strings.Repeat("2", 40), TokenDecimals: 6, AmountRaw: "6100000", Recipient: "0x" + strings.Repeat("3", 40)}
	seed := recovery.GasSeedPlan{BaseAddressID: uuid.New(), BaseAddress: "0x" + strings.Repeat("4", 40), Nonce: &nonce, AmountWei: "300", MaxFeeWei: "100"}
	proposal := recovery.Proposal{Binding: b, GasSeed: &seed, Fees: recovery.FeeEnvelope{MaxNativeFeeWei: "250"}, PreparedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(20 * time.Minute)}
	data, _ := recovery.Canonical(proposal)
	bindingJSON, _ := recovery.Canonical(b)
	bindingHash, _ := recovery.Hash(b)
	proposalHash, _ := recovery.Hash(proposal)
	reviewJSON, _ := recovery.Canonical(recovery.Review{ProposalHash: proposalHash, EvidenceHash: strings.Repeat("a", 64), ReviewedBy: "synthetic-independent", ReviewedAt: proposal.PreparedAt, ExpiresAt: proposal.ExpiresAt, Approved: true})
	row := &models.WithdrawalRecoveryAttempt{ID: uuid.New(), OperationID: b.OperationID, OriginalTransactionID: b.OriginalTransactionID, AccountID: b.AccountID, WalletID: b.WalletID, SourceAddressID: b.SourceAddressID, BindingJSON: bindingJSON, BindingHash: bindingHash, ProposalJSON: data, ProposalHash: proposalHash, ReviewJSON: reviewJSON, State: models.RecoveryGasClaimed}
	stub := &gasPreparerStub{quote: &sweep.DirectChildGasPlan{BaseAddressID: seed.BaseAddressID, BaseAddress: seed.BaseAddress, AmountWei: "260", MaxFeeWei: "90", MinFunderBalanceWei: "1000", TokenFeeWei: "210"}, observed: &sweep.DirectChildGasResult{}}
	bridge := &recoveryGasBridge{preparer: stub, observer: gasObserverStub{recovery.Observation{RPCsAgree: true, SourceNativeBalanceWei: "0", Quote: recovery.FeeQuote{GasLimit: 100, GasPriceWei: "2", L1FeeWei: "10"}}}, guard: func(context.Context) error { return nil }}
	networkID := big.NewInt(b.NetworkID)
	if !networkID.IsUint64() || networkID.Sign() == 0 {
		t.Fatal("synthetic network must be positive")
	}
	for i := range bridge.endpoints {
		bridge.endpoints[i] = runtime.Endpoint{Client: &gasRPCStub{network: networkID.Uint64()}}
	}
	return bridge, stub, row, b, seed
}
func TestRecoveryGasPreparationPreservesReviewedFinancialFields(t *testing.T) {
	bridge, stub, row, b, p := gasBridgeFixture(t)
	_, err := bridge.Prepare(context.Background(), row, b, p, "synthetic-passphrase")
	require.NoError(t, err)
	require.Equal(t, 1, stub.prepares)
	require.Equal(t, row.ID, stub.bound.ClaimID)
	require.Equal(t, b.Nonce, stub.bound.SourceNonce)
	require.Equal(t, p.BaseAddressID, stub.prepared.BaseAddressID)
	require.Equal(t, p.BaseAddress, stub.prepared.BaseAddress)
	require.Equal(t, p.AmountWei, stub.prepared.AmountWei)
	require.Equal(t, p.MaxFeeWei, stub.prepared.MaxFeeWei)
	require.Equal(t, p.Nonce, stub.prepared.FunderNonce)
	require.Equal(t, "1000", stub.prepared.MinFunderBalanceWei)
	require.Equal(t, "210", stub.prepared.TokenFeeWei)
	var proposal recovery.Proposal
	require.NoError(t, json.Unmarshal([]byte(row.ProposalJSON), &proposal))
	require.Equal(t, proposal.ExpiresAt, stub.prepared.ExpiresAt)
}
func TestRecoveryGasPreparationRefusesDriftBeforeSigningOrSending(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*recoveryGasBridge, *gasPreparerStub, *models.WithdrawalRecoveryAttempt, *recovery.GasSeedPlan)
	}{
		{"seed cost grew", func(_ *recoveryGasBridge, s *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			s.quote.AmountWei = "301"
		}},
		{"seed fee grew", func(_ *recoveryGasBridge, s *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			s.quote.MaxFeeWei = "101"
		}},
		{"token fee beyond review", func(_ *recoveryGasBridge, s *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			s.quote.TokenFeeWei = "251"
		}},
		{"base replaced", func(_ *recoveryGasBridge, s *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			s.quote.BaseAddressID = uuid.New()
		}},
		{"plan changed", func(_ *recoveryGasBridge, _ *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, p *recovery.GasSeedPlan) {
			p.AmountWei = "299"
		}},
		{"claim changed", func(_ *recoveryGasBridge, _ *gasPreparerStub, r *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			r.WalletID = uuid.New()
		}},
		{"RPC network drift", func(g *recoveryGasBridge, _ *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			g.endpoints[1].Client.(*gasRPCStub).network = 1
		}},
		{"pending funder nonce advanced", func(g *recoveryGasBridge, _ *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			g.endpoints[1].Client.(*gasRPCStub).pending = 1
		}},
		{"paused", func(g *recoveryGasBridge, _ *gasPreparerStub, _ *models.WithdrawalRecoveryAttempt, _ *recovery.GasSeedPlan) {
			g.guard = func(context.Context) error { return errors.New("paused") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, s, r, b, p := gasBridgeFixture(t)
			tc.mutate(g, s, r, &p)
			_, err := g.Prepare(context.Background(), r, b, p, "synthetic")
			require.Error(t, err)
			require.Zero(t, s.prepares+s.resumes)
		})
	}
}
func TestRecoveryGasObserveNeverSendsAndResumeDistinguishesMissingFromIncluded(t *testing.T) {
	for _, tc := range []struct {
		name            string
		observed        *sweep.DirectChildGasResult
		err             error
		prepare, resume int
	}{
		{"missing", nil, models.ErrRepositoryNotFound, 1, 0},
		{"pending", &sweep.DirectChildGasResult{}, nil, 0, 1},
		{"included gas spent", &sweep.DirectChildGasResult{Included: true}, nil, 0, 0},
		{"ready", &sweep.DirectChildGasResult{Included: true, Confirmed: true}, nil, 0, 0},
		{"failed", &sweep.DirectChildGasResult{}, sweep.ErrGasPreparationPending, 0, 0},
		{"corrupt", nil, sweep.ErrGasPreparationBinding, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, s, r, b, p := gasBridgeFixture(t)
			s.observed = tc.observed
			s.observeErr = tc.err
			_, _ = g.Observe(context.Background(), r)
			require.Zero(t, s.prepares+s.resumes)
			_, _ = g.Resume(context.Background(), r, b, p, "synthetic")
			require.Equal(t, tc.prepare, s.prepares)
			require.Equal(t, tc.resume, s.resumes)
		})
	}
}
func TestRecoveryGasQuoteAvoidsSeedWhenChildCanPay(t *testing.T) {
	g, s, _, b, _ := gasBridgeFixture(t)
	g.observer = gasObserverStub{recovery.Observation{RPCsAgree: true, SourceNativeBalanceWei: "210", Quote: recovery.FeeQuote{GasLimit: 100, GasPriceWei: "2", L1FeeWei: "10"}}}
	p, err := g.quote(context.Background(), b)
	require.NoError(t, err)
	require.Nil(t, p)
	require.Zero(t, s.quotes)
	g.observer = gasObserverStub{recovery.Observation{RPCsAgree: true, SourceNativeBalanceWei: "209", Quote: recovery.FeeQuote{GasLimit: 100, GasPriceWei: "2", L1FeeWei: "10"}}}
	p, err = g.quote(context.Background(), b)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.NotNil(t, p.Nonce)
	require.Zero(t, *p.Nonce)
	require.Equal(t, b.OperationID, s.bound.ClaimID)
	require.Zero(t, s.prepares+s.resumes)
}

func TestRecoveryGasPreservesReviewedNonzeroFunderNonce(t *testing.T) {
	g, s, r, b, p := gasBridgeFixture(t)
	for _, endpoint := range g.endpoints {
		rpc := endpoint.Client.(*gasRPCStub)
		rpc.latest = 7
		rpc.pending = 7
	}
	quote, err := g.quote(context.Background(), b)
	require.NoError(t, err)
	require.Equal(t, uint64(7), *quote.Nonce)
	var proposal recovery.Proposal
	require.NoError(t, json.Unmarshal([]byte(r.ProposalJSON), &proposal))
	p.Nonce = quote.Nonce
	proposal.GasSeed = &p
	r.ProposalJSON, _ = recovery.Canonical(proposal)
	r.ProposalHash, _ = recovery.Hash(proposal)
	var review recovery.Review
	require.NoError(t, json.Unmarshal([]byte(r.ReviewJSON), &review))
	review.ProposalHash = r.ProposalHash
	r.ReviewJSON, _ = recovery.Canonical(review)
	_, err = g.Prepare(context.Background(), r, b, p, "synthetic")
	require.NoError(t, err)
	require.Equal(t, uint64(7), *s.prepared.FunderNonce)
	g.endpoints[1].Client.(*gasRPCStub).pending = 8
	_, err = g.Prepare(context.Background(), r, b, p, "synthetic")
	require.ErrorIs(t, err, recovery.ErrNeedsReconciliation)
	require.Equal(t, 1, s.prepares)
}

func TestRecoveryGasBindingRejectsUnsupportedPrecisionBeforeConversion(t *testing.T) {
	for _, precision := range []int{-1, 37, 256} {
		_, err := directRecoveryGasBinding(recovery.Binding{TokenDecimals: precision}, uuid.New())
		require.ErrorIs(t, err, recovery.ErrRefused)
	}
	for _, precision := range []int{0, 36} {
		binding, err := directRecoveryGasBinding(recovery.Binding{TokenDecimals: precision}, uuid.New())
		require.NoError(t, err)
		require.Equal(t, precision, int(binding.TokenDecimals))
	}
}

func TestRecoveryGasUsesShorterReviewDeadlineAndRefusesExpiredFinalRPC(t *testing.T) {
	for _, crossExpiry := range []bool{false, true} {
		t.Run(fmt.Sprint(crossExpiry), func(t *testing.T) {
			g, stub, row, b, p := gasBridgeFixture(t)
			var proposal recovery.Proposal
			require.NoError(t, json.Unmarshal([]byte(row.ProposalJSON), &proposal))
			current := proposal.PreparedAt
			g.now = func() time.Time { return current }
			var review recovery.Review
			require.NoError(t, json.Unmarshal([]byte(row.ReviewJSON), &review))
			review.ExpiresAt = current.Add(time.Second)
			row.ReviewJSON, _ = recovery.Canonical(review)
			if crossExpiry {
				g.endpoints[1].Client = &expiryGasRPC{gasRPCStub: gasRPCStub{network: 421614}, cross: func() { current = current.Add(2 * time.Second) }}
			}
			_, err := g.Prepare(context.Background(), row, b, p, "synthetic")
			if crossExpiry {
				require.ErrorIs(t, err, recovery.ErrRefused)
				require.Zero(t, stub.prepares+stub.resumes)
			} else {
				require.NoError(t, err)
				require.Equal(t, review.ExpiresAt, stub.prepared.ExpiresAt)
				require.True(t, stub.prepared.ExpiresAt.Before(proposal.ExpiresAt))
			}
		})
	}
}

type expiryGasRPC struct {
	gasRPCStub
	cross func()
}

func (r *expiryGasRPC) Call(ctx context.Context, method string, out interface{}, params ...interface{}) error {
	err := r.gasRPCStub.Call(ctx, method, out, params...)
	if method == "eth_getTransactionCount" && params[1] == "pending" {
		r.cross()
	}
	return err
}
func TestRecoveryGasPrivateSendGateKeepsIndependentReviewDeadline(t *testing.T) {
	now := time.Now().UTC()
	ctx := withRecoveryReviewDeadline(context.Background(), now.Add(time.Second))
	require.NoError(t, gateRecoveryReviewDeadline(ctx, now))
	require.ErrorIs(t, gateRecoveryReviewDeadline(ctx, now.Add(2*time.Second)), recovery.ErrRefused)
}

func TestRecoveryGasResumeDoesNotSendAfterShortReviewExpiresDuringObservation(t *testing.T) {
	g, stub, row, b, p := gasBridgeFixture(t)
	var proposal recovery.Proposal
	require.NoError(t, json.Unmarshal([]byte(row.ProposalJSON), &proposal))
	current := proposal.PreparedAt
	g.now = func() time.Time { return current }
	var review recovery.Review
	require.NoError(t, json.Unmarshal([]byte(row.ReviewJSON), &review))
	review.ExpiresAt = current.Add(time.Second)
	row.ReviewJSON, _ = recovery.Canonical(review)
	stub.beforeObserve = func() { current = current.Add(2 * time.Second) }
	_, err := g.Resume(context.Background(), row, b, p, "synthetic")
	require.ErrorIs(t, err, recovery.ErrRefused)
	require.Zero(t, stub.prepares+stub.resumes)
}
