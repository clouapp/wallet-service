package withdrawrecovery

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// journalGas models the gas port's required physical journal protocol, not an
// inference from RPC absence. A committed step has one immutable sealed payload;
// an explicit continuation can create a missing step or send only those bytes.
type journalGas struct {
	mu                                                  sync.Mutex
	bindingHash, planHash                               string
	attemptID                                           uuid.UUID
	persisted                                           bool
	ready                                               bool
	beforeCommitFailure                                 bool
	afterCommitFailure                                  bool
	unknownSend                                         bool
	prepareCalls, resumeCalls, observeCalls, signatures int
	submissions                                         []string
	result                                              GasResult
	sealed                                              string
	onReady                                             func()
	afterObserve                                        func()
}

func (j *journalGas) validate(row *models.WithdrawalRecoveryAttempt, b Binding, p GasSeedPlan) error {
	bh, _ := Hash(b)
	ph, _ := Hash(p)
	if row == nil || row.State != models.RecoveryGasClaimed || bh != j.bindingHash || ph != j.planHash || (j.attemptID != uuid.Nil && row.ID != j.attemptID) {
		return ErrRefused
	}
	return nil
}
func (j *journalGas) Prepare(_ context.Context, row *models.WithdrawalRecoveryAttempt, b Binding, p GasSeedPlan, _ string) (*GasResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.prepareCalls++
	if err := j.validate(row, b, p); err != nil {
		return nil, err
	}
	if j.persisted {
		return j.proof(), nil
	}
	return j.createExact(row, b, p)
}
func (j *journalGas) createExact(row *models.WithdrawalRecoveryAttempt, b Binding, p GasSeedPlan) (*GasResult, error) {
	if j.beforeCommitFailure {
		return nil, errors.New("fee read failed before step commit")
	}
	n := *p.Nonce
	j.result = GasResult{TransactionID: uuid.NewSHA1(b.OperationID, []byte("same-reviewed-gas-step")), TxHash: "0x" + strings.Repeat("d", 64), NetworkID: b.NetworkID, From: p.BaseAddress, To: b.SourceAddress, Nonce: &n, AmountWei: p.AmountWei, FeeWei: "1000"}
	j.attemptID = row.ID
	j.persisted = true
	j.signatures++
	j.sealed = "one-signed-seed-at-the-reviewed-nonce"
	if j.afterCommitFailure {
		return nil, errors.New("interrupted after sealed step commit")
	}
	return j.sendExact()
}
func (j *journalGas) sendExact() (*GasResult, error) {
	j.submissions = append(j.submissions, j.sealed)
	if j.unknownSend {
		return nil, errors.New("seed broadcast answer unknown")
	}
	j.ready = true
	if j.onReady != nil {
		j.onReady()
	}
	return j.proof(), nil
}
func (j *journalGas) proof() *GasResult {
	if !j.persisted {
		return nil
	}
	copy := j.result
	copy.Confirmed = j.ready
	return &copy
}
func (j *journalGas) Observe(_ context.Context, _ *models.WithdrawalRecoveryAttempt) (*GasResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.observeCalls++
	if j.afterObserve != nil {
		j.afterObserve()
	}
	if !j.persisted {
		return nil, ErrNotFound
	}
	return j.proof(), nil
}
func (j *journalGas) Resume(_ context.Context, row *models.WithdrawalRecoveryAttempt, b Binding, p GasSeedPlan, _ string) (*GasResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.resumeCalls++
	if err := j.validate(row, b, p); err != nil {
		return nil, err
	}
	if !j.persisted {
		return j.createExact(row, b, p)
	}
	if j.ready {
		return j.proof(), nil
	}
	return j.sendExact()
}
func gasResumeFixture(t *testing.T) (*fixture, *journalGas, *GasSeedPlan) {
	f := newFixture(t)
	f.observer.ob.SourceNativeBalanceWei = "0"
	n := uint64(0)
	seed := &GasSeedPlan{BaseAddressID: uuid.New(), BaseAddress: "0x4444444444444444444444444444444444444444", Nonce: &n, AmountWei: "150000", MaxFeeWei: "10000"}
	bh, _ := Hash(f.binding)
	ph, _ := Hash(seed)
	j := &journalGas{bindingHash: bh, planHash: ph, onReady: func() { f.observer.ob.SourceNativeBalanceWei = "150000" }}
	f.s.deps.Gas = j
	return f, j, seed
}

func TestGasResumeAfterPrecommitFailureCreatesOnlyOriginalReviewedStep(t *testing.T) {
	f, j, seed := gasResumeFixture(t)
	j.beforeCommitFailure = true
	p, r := f.proposal(t, seed)
	out, err := f.s.Apply(context.Background(), p, r, "operator", "test")
	if err == nil || out.State != models.RecoveryGasClaimed || j.persisted || j.signatures != 0 || len(j.submissions) != 0 || f.signer.calls != 0 {
		t.Fatalf("precommit failure escaped %#v %v", out, err)
	}
	// A caller's changed proposal object cannot change the durable plan used by
	// Resume: it is read from the attempt, never from a fresh caller quote.
	changed := uint64(7)
	seed.Nonce = &changed
	seed.AmountWei = "999999"
	j.beforeCommitFailure = false
	out, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || out.State != models.RecoveryBroadcast || j.resumeCalls != 1 || j.signatures != 1 || len(j.submissions) != 1 || *j.result.Nonce != 0 || j.result.AmountWei != "150000" || f.signer.calls != 1 || f.sender.calls != 1 {
		t.Fatalf("same reviewed step did not resume %#v %v", out, err)
	}
	if len(f.store.rows) != 1 {
		t.Fatal("resumed gas created another business operation")
	}
}

func TestGasResumeAfterStepCommitNeverSignsAnotherSeed(t *testing.T) {
	for _, phase := range []string{"before_send", "unknown_reply"} {
		t.Run(phase, func(t *testing.T) {
			f, j, seed := gasResumeFixture(t)
			if phase == "before_send" {
				j.afterCommitFailure = true
			} else {
				j.unknownSend = true
			}
			p, r := f.proposal(t, seed)
			out, err := f.s.Apply(context.Background(), p, r, "operator", "test")
			if err == nil || out.State != models.RecoveryGasClaimed || !j.persisted || j.signatures != 1 {
				t.Fatalf("committed gas step lost %#v %v", out, err)
			}
			id, hash, nonce, amount := j.result.TransactionID, j.result.TxHash, *j.result.Nonce, j.result.AmountWei
			j.afterCommitFailure = false
			j.unknownSend = false
			out, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
			if err != nil || out.State != models.RecoveryBroadcast || j.signatures != 1 || j.result.TransactionID != id || j.result.TxHash != hash || *j.result.Nonce != nonce || j.result.AmountWei != amount {
				t.Fatalf("committed gas was replaced %#v %v", out, err)
			}
			for _, raw := range j.submissions {
				if raw != j.sealed {
					t.Fatal("seed replay changed signed bytes")
				}
			}
			want := 1
			if phase == "unknown_reply" {
				want = 2
			}
			if len(j.submissions) != want {
				t.Fatalf("submissions %d want%d", len(j.submissions), want)
			}
		})
	}
}

func TestGasResumeOriginalAppearsDuringObserveStopsBeforeContinuation(t *testing.T) {
	f, j, seed := gasResumeFixture(t)
	j.beforeCommitFailure = true
	p, r := f.proposal(t, seed)
	_, err := f.s.Apply(context.Background(), p, r, "operator", "test")
	if err == nil {
		t.Fatal("expected interrupted initial preparation")
	}
	j.beforeCommitFailure = false
	j.afterObserve = func() { f.observer.ob.OriginalKnown = true }
	out, err := f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if !errors.Is(err, ErrNeedsReconciliation) || out.State != models.RecoveryNeedsReconciliation || j.resumeCalls != 0 || j.signatures != 0 || len(j.submissions) != 0 || f.signer.calls != 0 {
		t.Fatalf("original winner ignored %#v %v", out, err)
	}
}

func TestGasResumeExpiredReviewCannotContinueEvenWithFreshRPCObservation(t *testing.T) {
	f, j, seed := gasResumeFixture(t)
	j.beforeCommitFailure = true
	p, r := f.proposal(t, seed)
	_, err := f.s.Apply(context.Background(), p, r, "operator", "test")
	if err == nil {
		t.Fatal("expected interrupted initial preparation")
	}
	j.beforeCommitFailure = false
	j.afterObserve = func() { f.now = f.now.Add(MaxReviewAge + MaxObservationAge); f.observer.ob.ObservedAt = f.now }
	out, err := f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if !errors.Is(err, ErrRefused) || out.State != models.RecoveryGasClaimed || j.resumeCalls != 0 || j.signatures != 0 || len(j.submissions) != 0 {
		t.Fatalf("expired review continued %#v %v", out, err)
	}
}

func TestReviewExpiryDuringFinalObservationNeverTransmits(t *testing.T) {
	f := newFixture(t)
	f.observer.hook = func(n int, o *Observation) {
		if n >= 6 {
			f.now = f.now.Add(MaxReviewAge + MaxObservationAge)
			o.ObservedAt = f.now
		}
	}
	out, err := f.apply(t)
	if !errors.Is(err, ErrRefused) || out.State != models.RecoverySigned || f.signer.calls != 1 || f.sender.calls != 0 || len(f.store.candidates) != 1 {
		t.Fatalf("expired send claim transmitted %#v %v", out, err)
	}
}
