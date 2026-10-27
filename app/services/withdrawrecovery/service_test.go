package withdrawrecovery

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	evmtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type memoryStore struct {
	mu                  sync.Mutex
	rows                map[uuid.UUID]*models.WithdrawalRecoveryAttempt
	candidates          []*models.Transaction
	claims, transitions int
	claimErr, recordErr error
	failTransition      string
}

func cloneAttempt(a *models.WithdrawalRecoveryAttempt) *models.WithdrawalRecoveryAttempt {
	if a == nil {
		return nil
	}
	b := *a
	return &b
}
func (m *memoryStore) FindByOperation(_ context.Context, id uuid.UUID) (*models.WithdrawalRecoveryAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[id]
	if r == nil {
		return nil, ErrNotFound
	}
	return cloneAttempt(r), nil
}
func (m *memoryStore) Claim(_ context.Context, row *models.WithdrawalRecoveryAttempt) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claims++
	if m.claimErr != nil {
		return false, m.claimErr
	}
	for _, v := range m.rows {
		if v.OperationID == row.OperationID || (v.NetworkID == row.NetworkID && v.Sender == row.Sender && v.SenderNonce == row.SenderNonce) {
			return false, nil
		}
	}
	m.rows[row.OperationID] = cloneAttempt(row)
	return true, nil
}
func (m *memoryStore) Transition(_ context.Context, id uuid.UUID, from, to string, patch models.WithdrawalRecoveryUpdate) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if to == m.failTransition {
		return false, errors.New("durability unavailable")
	}
	for _, r := range m.rows {
		if r.ID != id {
			continue
		}
		if r.State != from {
			return false, nil
		}
		m.transitions++
		r.State = to
		if patch.CallJSON != nil {
			r.CallJSON = *patch.CallJSON
		}
		if patch.GasResultJSON != nil {
			r.GasResultJSON = *patch.GasResultJSON
		}
		if patch.ObservationJSON != nil {
			r.ObservationJSON = *patch.ObservationJSON
		}
		return true, nil
	}
	return false, ErrNotFound
}
func (m *memoryStore) RecordCandidate(_ context.Context, expected *models.WithdrawalRecoveryAttempt, tx *models.Transaction, sealed string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recordErr != nil {
		return false, m.recordErr
	}
	r := m.rows[expected.OperationID]
	if r == nil || r.State != models.RecoverySigning || r.CandidateTransactionID != nil {
		return false, nil
	}
	r.State = models.RecoverySigned
	r.CandidateTransactionID = &tx.ID
	r.CandidateHash = tx.TxHash
	r.SealedSignedTx = sealed
	c := *tx
	m.candidates = append(m.candidates, &c)
	return true, nil
}

type fakeObserver struct {
	mu    sync.Mutex
	ob    Observation
	calls int
	hook  func(int, *Observation)
}

func (o *fakeObserver) Observe(_ context.Context, _ Binding, _ string) (*Observation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	ob := o.ob
	if o.hook != nil {
		o.hook(o.calls, &ob)
	}
	return &ob, nil
}

type fakeSigner struct {
	mu     sync.Mutex
	key    *ecdsa.PrivateKey
	calls  int
	mutate func(chain.EVMCall) chain.EVMCall
	err    error
}

func (s *fakeSigner) SignEVMRecoveryCall(_ context.Context, _, _ uuid.UUID, network int64, c chain.EVMCall, _ string) (*types.SignedTx, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if s.mutate != nil {
		c = s.mutate(c)
	}
	to := common.HexToAddress(c.To)
	tx := evmtypes.NewTx(&evmtypes.LegacyTx{Nonce: c.Nonce, To: &to, Value: c.Value, Data: c.Data, Gas: c.GasLimit, GasPrice: c.GasPrice})
	signed, err := evmtypes.SignTx(tx, evmtypes.LatestSignerForChainID(big.NewInt(network)), s.key)
	if err != nil {
		return nil, err
	}
	raw, _ := signed.MarshalBinary()
	return &types.SignedTx{RawBytes: raw, TxHash: signed.Hash().Hex()}, nil
}

type fakeSealer struct{ fail bool }

func (s *fakeSealer) Seal(raw string) (string, error) {
	if s.fail {
		return "", errors.New("secret sealing unavailable")
	}
	return "sealed:" + raw, nil
}
func (*fakeSealer) Open(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, "sealed:") {
		return "", errors.New("invalid seal")
	}
	return strings.TrimPrefix(sealed, "sealed:"), nil
}

type fakeSender struct {
	mu        sync.Mutex
	calls     int
	raw       [][]byte
	err       error
	wrongHash bool
}

func (s *fakeSender) SendExact(_ context.Context, _ Binding, raw []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.raw = append(s.raw, append([]byte(nil), raw...))
	if s.err != nil {
		return "", s.err
	}
	var tx evmtypes.Transaction
	_ = tx.UnmarshalBinary(raw)
	if s.wrongHash {
		return "0x" + strings.Repeat("b", 64), nil
	}
	return tx.Hash().Hex(), nil
}

type fakeGas struct {
	calls, observes int
	result          *GasResult
	err             error
	hook            func()
}

func (g *fakeGas) Prepare(_ context.Context, _ *models.WithdrawalRecoveryAttempt, _ Binding, _ GasSeedPlan, _ string) (*GasResult, error) {
	g.calls++
	if g.hook != nil {
		g.hook()
	}
	return g.result, g.err
}
func (g *fakeGas) Observe(_ context.Context, _ *models.WithdrawalRecoveryAttempt) (*GasResult, error) {
	g.observes++
	return g.result, g.err
}

type fixture struct {
	s        *Service
	store    *memoryStore
	observer *fakeObserver
	signer   *fakeSigner
	sender   *fakeSender
	sealer   *fakeSealer
	gas      *fakeGas
	binding  Binding
	fees     FeeEnvelope
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	nonce := uint64(0)
	now := time.Date(2026, 10, 27, 7, 30, 0, 0, time.UTC)
	b := Binding{OperationID: uuid.New(), OriginalTransactionID: uuid.New(), OriginalTxHash: "0x" + strings.Repeat("a", 64), AccountID: uuid.New(), WalletID: uuid.New(), SourceAddressID: uuid.New(), SourceAddress: crypto.PubkeyToAddress(key.PublicKey).Hex(), SourceDerivationIndex: 1, SourceExternalUserID: "2", Chain: "arbitrum", NetworkID: 421614, Nonce: &nonce, Asset: "USDC", TokenContract: models.USDCContractArbSepolia, TokenDecimals: 6, AmountRaw: "6100000", Recipient: "0x2222222222222222222222222222222222222222", RequestHash: strings.Repeat("a", 64), ProviderFingerprint: strings.Repeat("b", 64), RequiredConfirmations: 120, Finality: models.FinalityConfirmations}
	fees := FeeEnvelope{MaxGasLimit: 100000, MaxGasPriceWei: "100", MaxNativeFeeWei: "10000000"}
	o := &fakeObserver{ob: Observation{Binding: b, ObservedAt: now, AccountEnvironment: "test", WalletStatus: "active", ChainTestnet: true, AdapterType: "evm", RPCNetworkID: 421614, RPCsAgree: true, OriginalStatus: "failed", BroadcastOutcome: "refused", LatestNonce: &nonce, PendingNonce: &nonce, SourceTokenBalanceRaw: "11000000", SourceNativeBalanceWei: "200000", Quote: FeeQuote{GasLimit: 60000, GasPriceWei: "2", L1FeeWei: "7"}}}
	store := &memoryStore{rows: map[uuid.UUID]*models.WithdrawalRecoveryAttempt{}}
	signer := &fakeSigner{key: key}
	sender := &fakeSender{}
	sealer := &fakeSealer{}
	gas := &fakeGas{}
	f := &fixture{store: store, observer: o, signer: signer, sender: sender, sealer: sealer, gas: gas, binding: b, fees: fees, now: now}
	f.s, err = NewService(Dependencies{Store: store, Observer: o, Signer: signer, Sender: sender, Sealer: sealer, Gas: gas, Now: func() time.Time { return f.now }, CodeRevision: "reviewed-test-revision"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) proposal(t *testing.T, seed *GasSeedPlan) (Proposal, Review) {
	t.Helper()
	p, err := f.s.Inspect(context.Background(), f.binding, f.fees, seed, "preparer")
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := Hash(p)
	r := Review{ProposalHash: hash, EvidenceHash: strings.Repeat("c", 64), ReviewedBy: "independent-reviewer", ReviewedAt: f.now, ExpiresAt: p.ExpiresAt, Approved: true}
	return *p, r
}
func (f *fixture) apply(t *testing.T) (*Result, error) {
	p, r := f.proposal(t, nil)
	return f.s.Apply(context.Background(), p, r, "operator", "test-passphrase")
}
func assertNoEffects(t *testing.T, f *fixture) {
	t.Helper()
	if len(f.store.rows) != 0 || f.signer.calls != 0 || f.sender.calls != 0 || f.gas.calls != 0 {
		t.Fatalf("unexpected claim/sign/send/seed: %d/%d/%d/%d", len(f.store.rows), f.signer.calls, f.sender.calls, f.gas.calls)
	}
}

func TestInspectNeverClaimsOrSigns(t *testing.T) {
	f := newFixture(t)
	p, err := f.s.Inspect(context.Background(), f.binding, f.fees, nil, "preparer")
	if err != nil || p.Binding.Nonce == nil || *p.Binding.Nonce != 0 {
		t.Fatalf("nonce zero lost: %#v %v", p, err)
	}
	encoded, _ := json.Marshal(p)
	var round Proposal
	if json.Unmarshal(encoded, &round) != nil || round.Binding.Nonce == nil || *round.Binding.Nonce != 0 {
		t.Fatal("nonce0 serialization changed")
	}
	assertNoEffects(t, f)
}
func TestApplyKeepsBusinessUUIDRawUnitsAndExactNonceZero(t *testing.T) {
	f := newFixture(t)
	r, err := f.apply(t)
	if err != nil || r.State != models.RecoveryBroadcast {
		t.Fatalf("result %#v %v", r, err)
	}
	if f.signer.calls != 1 || f.sender.calls != 1 || len(f.store.candidates) != 1 {
		t.Fatal("not one signature/send/candidate")
	}
	tx := f.store.candidates[0]
	if tx.Amount != "6100000" || tx.IdempotencyKey == nil || *tx.IdempotencyKey != f.binding.OperationID.String() || tx.ParentTransactionID == nil || *tx.ParentTransactionID != f.binding.OriginalTransactionID {
		t.Fatalf("raw units or lineage changed: %#v", tx)
	}
	var decoded evmtypes.Transaction
	_ = decoded.UnmarshalBinary(f.sender.raw[0])
	if decoded.Nonce() != 0 {
		t.Fatal("nonce advanced")
	}
	public, _ := json.Marshal(r)
	if strings.Contains(string(public), "sealed") || strings.Contains(string(public), "passphrase") {
		t.Fatal("result leaked secret")
	}
}
func TestReviewAndBindingRefusalsHaveNoSideEffects(t *testing.T) {
	cases := map[string]func(*fixture, *Proposal, *Review){"nonce_missing": func(_ *fixture, p *Proposal, _ *Review) { p.Binding.Nonce = nil }, "mainnet": func(_ *fixture, p *Proposal, _ *Review) { p.Binding.NetworkID = 42161 }, "changed_amount": func(_ *fixture, p *Proposal, _ *Review) { p.Binding.AmountRaw = "6200000" }, "self_review": func(_ *fixture, _ *Proposal, r *Review) { r.ReviewedBy = "operator" }, "stale_review": func(f *fixture, _ *Proposal, _ *Review) { f.now = f.now.Add(MaxReviewAge + time.Second) }, "different_revision": func(f *fixture, _ *Proposal, _ *Review) { f.s.deps.CodeRevision = "another" }, "unapproved": func(_ *fixture, _ *Proposal, r *Review) { r.Approved = false }, "rpc_disagreement": func(f *fixture, _ *Proposal, _ *Review) { f.observer.ob.RPCsAgree = false }, "wrong_environment": func(f *fixture, _ *Proposal, _ *Review) { f.observer.ob.AccountEnvironment = "prod" }, "frozen": func(f *fixture, _ *Proposal, _ *Review) { f.observer.ob.WalletFrozen = true }, "new_nonce": func(f *fixture, _ *Proposal, _ *Review) { n := uint64(1); f.observer.ob.PendingNonce = &n }, "original_known": func(f *fixture, _ *Proposal, _ *Review) { f.observer.ob.OriginalKnown = true }, "changed_contract": func(f *fixture, _ *Proposal, _ *Review) {
		f.observer.ob.Binding.TokenContract = "0x3333333333333333333333333333333333333333"
	}}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			p, r := f.proposal(t, nil)
			mutate(f, &p, &r)
			_, err := f.s.Apply(context.Background(), p, r, "operator", "test")
			if err == nil {
				t.Fatal("accepted invalid recovery")
			}
			assertNoEffects(t, f)
		})
	}
}
func TestMissingNativeGasCannotInspectWithoutReviewedSeed(t *testing.T) {
	f := newFixture(t)
	f.observer.ob.SourceNativeBalanceWei = "0"
	_, err := f.s.Inspect(context.Background(), f.binding, f.fees, nil, "preparer")
	if !errors.Is(err, ErrGasNotReady) {
		t.Fatal(err)
	}
	assertNoEffects(t, f)
}
func TestClaimDurabilityFailurePreventsKeysGasAndSend(t *testing.T) {
	f := newFixture(t)
	p, r := f.proposal(t, nil)
	f.store.claimErr = errors.New("commit failed")
	if _, err := f.s.Apply(context.Background(), p, r, "operator", "test"); err == nil {
		t.Fatal("accepted failed claim")
	}
	assertNoEffects(t, f)
}
func TestReplayApplyAndConcurrentClaimHaveOneWinner(t *testing.T) {
	f := newFixture(t)
	p, r := f.proposal(t, nil)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.Apply(context.Background(), p, r, "operator", "test")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrOccupied) {
			t.Fatal(err)
		}
	}
	if wins != 1 || f.signer.calls != 1 || f.sender.calls != 1 {
		t.Fatalf("winners/sign/send %d/%d/%d", wins, f.signer.calls, f.sender.calls)
	}
}
func TestSignedCandidateMustCommitBeforeNetworkSend(t *testing.T) {
	f := newFixture(t)
	f.store.recordErr = errors.New("DB commit failed")
	r, err := f.apply(t)
	if err == nil || r.State != models.RecoverySigning || f.sender.calls != 0 {
		t.Fatalf("broadcast without durable candidate: %#v %v", r, err)
	}
	f.store.recordErr = nil
	r, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || r.State != models.RecoveryBroadcast || f.sender.calls != 1 {
		t.Fatalf("frozen attempt could not resume %#v %v", r, err)
	}
	if len(f.store.rows) != 1 {
		t.Fatal("new attempt created")
	}
}
func TestSignedBytesSurviveUnknownAndResumeNeverSignsAgain(t *testing.T) {
	f := newFixture(t)
	f.sender.err = errors.New("connection reset after write")
	r, err := f.apply(t)
	if !errors.Is(err, ErrNeedsReconciliation) || r.State != models.RecoverySendUnknown {
		t.Fatalf("unknown lost %#v %v", r, err)
	}
	before, _ := f.store.FindByOperation(context.Background(), f.binding.OperationID)
	if before.SealedSignedTx == "" {
		t.Fatal("sealed bytes lost")
	}
	f.sender.err = nil
	r, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || r.State != models.RecoveryBroadcast || f.signer.calls != 1 || f.sender.calls != 2 {
		t.Fatalf("unknown was replanned %#v %v", r, err)
	}
	if hex.EncodeToString(f.sender.raw[0]) != hex.EncodeToString(f.sender.raw[1]) {
		t.Fatal("replay changed raw bytes")
	}
}
func TestPendingOriginalStopsBeforeAnyReplacementSignature(t *testing.T) {
	f := newFixture(t)
	p, r := f.proposal(t, nil)
	f.observer.hook = func(n int, o *Observation) {
		if n >= 3 {
			o.OriginalKnown = true
		}
	}
	out, err := f.s.Apply(context.Background(), p, r, "operator", "test")
	if !errors.Is(err, ErrNeedsReconciliation) || out.State != models.RecoveryNeedsReconciliation || f.signer.calls != 0 || f.sender.calls != 0 {
		t.Fatalf("original visibility ignored %#v %v", out, err)
	}
}
func TestChangedSignedEconomicsNeverReachNetwork(t *testing.T) {
	for name, mutate := range map[string]func(chain.EVMCall) chain.EVMCall{"nonce": func(c chain.EVMCall) chain.EVMCall { c.Nonce = 1; return c }, "recipient": func(c chain.EVMCall) chain.EVMCall { c.To = "0x3333333333333333333333333333333333333333"; return c }, "amount": func(c chain.EVMCall) chain.EVMCall { c.Data = append([]byte(nil), c.Data...); c.Data[67]++; return c }, "native_value": func(c chain.EVMCall) chain.EVMCall { c.Value = big.NewInt(1); return c }, "gas": func(c chain.EVMCall) chain.EVMCall { c.GasPrice = big.NewInt(101); return c }} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.signer.mutate = mutate
			_, err := f.apply(t)
			if err == nil || f.sender.calls != 0 || len(f.store.candidates) != 0 {
				t.Fatal("malicious signer accepted")
			}
		})
	}
}
func TestGasIsClaimedOnceAndOriginalWinningDuringSeedStopsSigning(t *testing.T) {
	f := newFixture(t)
	f.observer.ob.SourceNativeBalanceWei = "0"
	baseNonce := uint64(0)
	seed := &GasSeedPlan{BaseAddressID: uuid.New(), BaseAddress: "0x4444444444444444444444444444444444444444", Nonce: &baseNonce, AmountWei: "150000", MaxFeeWei: "10000"}
	f.gas.result = &GasResult{TransactionID: uuid.New(), TxHash: "0x" + strings.Repeat("d", 64), NetworkID: 421614, From: seed.BaseAddress, To: f.binding.SourceAddress, Nonce: &baseNonce, AmountWei: "150000", FeeWei: "1000", Confirmed: true}
	f.gas.hook = func() { f.observer.ob.SourceNativeBalanceWei = "150000"; f.observer.ob.OriginalKnown = true }
	p, r := f.proposal(t, seed)
	out, err := f.s.Apply(context.Background(), p, r, "operator", "test")
	if !errors.Is(err, ErrNeedsReconciliation) || out.State != models.RecoveryNeedsReconciliation || f.gas.calls != 1 || f.signer.calls != 0 || f.sender.calls != 0 {
		t.Fatalf("old winner after seed ignored %#v %v", out, err)
	}
}
func TestUnknownGasResultResumesByObservationOnly(t *testing.T) {
	f := newFixture(t)
	f.observer.ob.SourceNativeBalanceWei = "0"
	n := uint64(0)
	seed := &GasSeedPlan{BaseAddressID: uuid.New(), BaseAddress: "0x4444444444444444444444444444444444444444", Nonce: &n, AmountWei: "150000", MaxFeeWei: "10000"}
	f.gas.err = errors.New("unknown seed send")
	p, r := f.proposal(t, seed)
	out, err := f.s.Apply(context.Background(), p, r, "operator", "test")
	if err == nil || out.State != models.RecoveryGasClaimed {
		t.Fatal("gas claim not retained")
	}
	f.gas.err = nil
	f.gas.result = &GasResult{TransactionID: uuid.New(), TxHash: "0x" + strings.Repeat("d", 64), NetworkID: 421614, From: seed.BaseAddress, To: f.binding.SourceAddress, Nonce: &n, AmountWei: "150000", FeeWei: "1000", Confirmed: true}
	f.observer.ob.SourceNativeBalanceWei = "150000"
	out, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || out.State != models.RecoveryBroadcast || f.gas.calls != 1 || f.gas.observes != 1 || f.signer.calls != 1 {
		t.Fatalf("gas replayed or not resumed %#v %v", out, err)
	}
}
func TestAbsentHashIsNeverReportedAsFinalFailure(t *testing.T) {
	f := newFixture(t)
	p, _ := f.proposal(t, nil)
	if p.Observation.OriginalKnown || p.Observation.ReceiptObserved {
		t.Fatal("fixture expected absent hash")
	}
	assertNoEffects(t, f)
	f.observer.ob.LatestNonce = nil
	if _, err := f.s.Inspect(context.Background(), f.binding, f.fees, nil, "preparer"); err == nil {
		t.Fatal("missing nonce accepted from hash absence")
	}
}

func TestCrashAfterTransmissionClaimReplaysOnlyStoredBytes(t *testing.T) {
	f := newFixture(t)
	f.store.failTransition = models.RecoveryBroadcast
	out, err := f.apply(t)
	if err == nil || out.State != models.RecoverySendClaimed || f.signer.calls != 1 || f.sender.calls != 1 {
		t.Fatalf("expected interrupted send claim %#v %v", out, err)
	}
	f.store.failTransition = ""
	out, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || out.State != models.RecoveryBroadcast || f.signer.calls != 1 || f.sender.calls != 2 {
		t.Fatalf("resume generated new signature %#v %v", out, err)
	}
	if hex.EncodeToString(f.sender.raw[0]) != hex.EncodeToString(f.sender.raw[1]) {
		t.Fatal("interrupted transmission changed signed bytes")
	}
}

func TestGasDrainedAfterCandidateCommitPreventsSendAndKeepsSealedCandidate(t *testing.T) {
	f := newFixture(t)
	f.observer.hook = func(n int, o *Observation) {
		if n >= 6 {
			o.SourceNativeBalanceWei = "0"
		}
	}
	out, err := f.apply(t)
	if !errors.Is(err, ErrGasNotReady) || out.State != models.RecoverySigned || f.sender.calls != 0 || f.signer.calls != 1 {
		t.Fatalf("sent without final gas readiness %#v %v", out, err)
	}
	f.observer.hook = nil
	out, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || out.State != models.RecoveryBroadcast || f.signer.calls != 1 || f.sender.calls != 1 {
		t.Fatalf("candidate was replaced %#v %v", out, err)
	}
}

func TestNonceConsumedAfterSignatureNeverSignsNonceOne(t *testing.T) {
	f := newFixture(t)
	f.observer.hook = func(n int, o *Observation) {
		if n >= 5 {
			one := uint64(1)
			o.LatestNonce = &one
			o.PendingNonce = &one
		}
	}
	out, err := f.apply(t)
	if !errors.Is(err, ErrNeedsReconciliation) || out.State != models.RecoveryNeedsReconciliation || f.sender.calls != 0 || f.signer.calls != 1 {
		t.Fatalf("consumed nonce was ignored %#v %v", out, err)
	}
	out, err = f.s.Resume(context.Background(), f.binding.OperationID, "operator", "test")
	if err != nil || out.State != models.RecoveryNeedsReconciliation || f.signer.calls != 1 {
		t.Fatal("recovery advanced nonce")
	}
}
