package sweep

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type gasStoreContext struct{}
type memoryGasStore struct {
	*fakeTxRepo
	mu             sync.Mutex
	commits        atomic.Int64
	commitErr      error
	afterCommitErr error
}

func (m *memoryGasStore) WithinDirectChildGas(ctx context.Context, _, _ uuid.UUID, fn func(context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mark := len(m.created)
	if err := fn(context.WithValue(ctx, gasStoreContext{}, m)); err != nil {
		m.created = m.created[:mark]
		return err
	}
	if m.commitErr != nil {
		m.created = m.created[:mark]
		return m.commitErr
	}
	m.commits.Add(1)
	return m.afterCommitErr
}
func (m *memoryGasStore) FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	if ctx.Value(gasStoreContext{}) != m {
		m.mu.Lock()
		defer m.mu.Unlock()
	}
	for _, row := range m.created {
		if row.ID == id {
			copyRow := *row
			return &copyRow, nil
		}
	}
	return nil, models.ErrRepositoryNotFound
}
func (m *memoryGasStore) DirectChildGasNonceOccupied(ctx context.Context, except uuid.UUID, c, from string, nonce uint64) (bool, error) {
	if ctx.Value(gasStoreContext{}) != m {
		return false, errors.New("missing guarded transaction")
	}
	for _, row := range m.created {
		if row.ID == except || row.Chain != c || row.FromAddress != from {
			continue
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal([]byte(row.RawPayload), &raw)
		var used uint64
		_ = json.Unmarshal(raw[chain.SenderNoncePayloadKey], &used)
		if used == nonce {
			return true, nil
		}
	}
	return false, nil
}

type gasPrepFixture struct {
	svc     *service
	port    *tokenReadinessPort
	wallet  *models.Wallet
	child   models.Address
	store   *memoryGasStore
	key     *ecdsa.PrivateKey
	binding DirectChildGasBinding
	plan    *DirectChildGasPlan
	signs   atomic.Int64
	sends   atomic.Int64
}

func newGasPrepFixture(t *testing.T) *gasPrepFixture {
	t.Helper()
	svc, port, wallet, child, rows, _, _ := tokenReadinessFixture(t, 0, 1_000_000)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	oldBase := wallet.DepositAddress.Address
	wallet.DepositAddress.Address = crypto.PubkeyToAddress(key.PublicKey).Hex()
	port.balances[wallet.DepositAddress.Address] = port.balances[oldBase]
	delete(port.balances, oldBase)
	port.tokens[wallet.DepositAddress.Address] = new(big.Int)
	wallet.DepositAddress.Chain, wallet.DepositAddress.ExternalUserID, wallet.DepositAddress.IsActive = wallet.Chain, "system", true
	child.Chain, child.IsActive, child.ExternalUserID = wallet.Chain, true, "test-user"
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{*wallet.DepositAddress, child}}
	accountID := uuid.New()
	wallet.AccountID = &accountID
	wallet.Status = "active"
	network := int64(80002)
	svc.chainRepo = &fakeChainRepo{chain: &models.Chain{ID: wallet.Chain, AdapterType: models.AdapterTypeEVM, NetworkID: &network, Status: "active"}}
	port.nativeGasLimit, port.tokenGasLimit = 21_000, 65_000
	port.GetTransactionBlockVal = 0
	store := &memoryGasStore{fakeTxRepo: rows}
	svc.txRepo = store
	svc.sealer = testSealer{}
	op := uuid.New()
	f := &gasPrepFixture{svc: svc, port: port, wallet: wallet, child: child, store: store, key: key,
		binding: DirectChildGasBinding{OperationID: op, ClaimID: op, AccountID: accountID, WalletID: wallet.ID, SourceAddressID: child.ID,
			SourceAddress: child.Address, SourceDerivationIndex: child.DerivationIndex, SourceExternalUserID: child.ExternalUserID,
			Chain: wallet.Chain, NetworkID: network, Asset: gasPlanAsset, TokenContract: gasPlanToken.Contract, TokenDecimals: gasPlanToken.Decimals,
			AmountRaw: "6100000", Recipient: gasPlanDestination}}
	port.BuildTransferFn = func(context.Context, types.TransferRequest) (*types.UnsignedTx, error) {
		return nil, errors.New("unexpected embedded build")
	}
	port.BroadcastTransactionFn = func(context.Context, *types.SignedTx) (string, error) {
		if f.store.commits.Load() == 0 {
			t.Fatal("send before physical commit")
		}
		f.sends.Add(1)
		return "", chain.UnknownOutcome(errors.New("synthetic lost reply"))
	}
	f.plan, err = svc.QuoteDirectChildGas(context.Background(), f.binding)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// gasSeedPort overrides BuildTransfer with the same metadata the live EVM
// adapter encodes. Its real signatures are independently decoded and recovered.
type gasSeedPort struct {
	*tokenReadinessPort
	network int64
}

func (p *gasSeedPort) WithFeePolicy(policy chain.FeePolicy) types.Chain {
	copyPort := *p
	copyReadiness := *p.tokenReadinessPort
	copyEVM := *p.evmPlanPort
	copyEVM.fee, copyEVM.estimates, copyEVM.reserveRecipients = policy, nil, nil
	copyReadiness.evmPlanPort = &copyEVM
	copyPort.tokenReadinessPort = &copyReadiness
	return &copyPort
}
func (p *gasSeedPort) ValidateAddress(address string) bool { return common.IsHexAddress(address) }
func (p *gasSeedPort) BuildTransfer(_ context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	p.BuildTransferCalls++
	price := p.pricedGas()
	limit := p.nativeGasLimit
	return &types.UnsignedTx{ChainID: p.ID(), Metadata: map[string]interface{}{"nonce": uint64(0), "chain_id": p.network, "to": req.To, "value": req.Amount.String(), "gas_price": price.String(), "gas_limit": limit}}, nil
}

func (f *gasPrepFixture) installSeedPort() {
	port := &gasSeedPort{tokenReadinessPort: f.port, network: f.binding.NetworkID}
	registry := chain.NewRegistry()
	registry.RegisterChain(port)
	registry.RegisterToken(gasPlanToken)
	f.svc.registry = registry
}

func (f *gasPrepFixture) sign(_ context.Context, g *directGasContext, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	f.signs.Add(1)
	value, _ := new(big.Int).SetString(unsigned.Metadata["value"].(string), 10)
	price, _ := new(big.Int).SetString(unsigned.Metadata["gas_price"].(string), 10)
	tx := gethtypes.NewTransaction(unsigned.Metadata["nonce"].(uint64), common.HexToAddress(unsigned.Metadata["to"].(string)), value, unsigned.Metadata["gas_limit"].(uint64), price, nil)
	signed, err := gethtypes.SignTx(tx, gethtypes.LatestSignerForChainID(big.NewInt(*g.chain.NetworkID)), f.key)
	if err != nil {
		return nil, err
	}
	raw, err := signed.MarshalBinary()
	return &types.SignedTx{ChainID: f.binding.Chain, TxHash: signed.Hash().Hex(), RawBytes: raw}, err
}

func TestDirectChildGas_CommitsExactSealedSeedBeforeSendAndObserveNeverSends(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, err := f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	if !errors.Is(err, ErrGasPreparationPending) {
		t.Fatalf("unknown send: %v", err)
	}
	if f.signs.Load() != 1 || f.sends.Load() != 1 || len(f.store.created) != 1 {
		t.Fatal("expected exactly one signed, committed seed")
	}
	row := f.store.created[0]
	if row.ID != DirectChildGasStepID(f.binding.OperationID) || row.IdempotencyKey == nil || *row.IdempotencyKey != row.ID.String() {
		t.Fatal("unstable seed identity")
	}
	result, err := f.svc.ObserveDirectChildGas(context.Background(), f.binding)
	if err != nil || result.Confirmed || result.Nonce == nil || *result.Nonce != 0 || f.sends.Load() != 1 {
		t.Fatalf("readonly observation: %+v %v", result, err)
	}
	_, err = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, func(context.Context, *directGasContext, *types.UnsignedTx) (*types.SignedTx, error) {
		t.Fatal("retry re-signed")
		return nil, nil
	})
	if err != nil || f.sends.Load() != 1 {
		t.Fatal("Prepare retransmitted an existing step")
	}
}

func TestDirectChildGas_CommitFailureSendsNothingAndCanRetry(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	f.store.commitErr = errors.New("synthetic commit failure")
	_, err := f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	if err == nil || f.sends.Load() != 0 || len(f.store.created) != 0 {
		t.Fatal("uncommitted bytes escaped")
	}
	f.store.commitErr = nil
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	if f.signs.Load() != 2 || f.sends.Load() != 1 || len(f.store.created) != 1 {
		t.Fatal("pre-commit rollback was not safely retryable")
	}
}

func TestDirectChildGas_LostCommitAckResumesOnlyCommittedBytes(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	f.store.afterCommitErr = errors.New("synthetic lost commit acknowledgement")
	_, err := f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	if err == nil || f.sends.Load() != 0 || len(f.store.created) != 1 {
		t.Fatal("lost commit acknowledgement did not stop send")
	}
	f.store.afterCommitErr = nil
	row := f.store.created[0]
	raw, _ := f.svc.keptSignedBytes(row)
	f.port.BroadcastTransactionFn = func(_ context.Context, signed *types.SignedTx) (string, error) {
		if signed.TxHash != row.TxHash || string(signed.RawBytes) != string(raw) {
			t.Fatal("resume changed bytes")
		}
		f.sends.Add(1)
		return "", chain.UnknownOutcome(errors.New("synthetic lost reply"))
	}
	_, _ = f.svc.ResumeDirectChildGas(context.Background(), f.binding)
	if f.signs.Load() != 1 || f.sends.Load() != 1 {
		t.Fatal("resume re-signed or failed to reuse its step")
	}
}

func TestDirectChildGas_ConcurrentPreparersSignAndTransmitOnlyOnce(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
		}()
	}
	close(start)
	wait.Wait()
	if f.signs.Load() != 1 || f.sends.Load() != 1 || len(f.store.created) != 1 {
		t.Fatalf("signs=%d sends=%d rows=%d", f.signs.Load(), f.sends.Load(), len(f.store.created))
	}
}

func TestDirectChildGas_PinsZeroNonceAndRefusesAnotherFunderNonceBeforeSigning(t *testing.T) {
	for _, nonce := range []uint64{0, 1} {
		f := newGasPrepFixture(t)
		f.installSeedPort()
		f.plan.FunderNonce = &nonce
		_, err := f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
		if nonce == 0 {
			if !errors.Is(err, ErrGasPreparationPending) || f.signs.Load() != 1 || f.sends.Load() != 1 {
				t.Fatalf("nonce0 was not preserved: %v", err)
			}
		} else if !errors.Is(err, ErrGasPreparationBinding) || f.signs.Load() != 0 || f.sends.Load() != 0 || len(f.store.created) != 0 {
			t.Fatalf("mismatched funder nonce reached signing: %v", err)
		}
	}
}

func TestDirectChildGas_AnotherOperationCannotReclaimTheFunderNonce(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	other := f.binding
	other.OperationID = uuid.New()
	other.ClaimID = other.OperationID
	_, err := f.svc.prepareDirectChildGas(context.Background(), other, *f.plan, f.sign)
	if !errors.Is(err, ErrGasPreparationPending) || f.signs.Load() != 1 || len(f.store.created) != 1 {
		t.Fatalf("occupied nonce was re-signed: %v", err)
	}
}

func TestDirectChildGas_BindingDriftCannotObserveResumeOrPrepareAnotherSeed(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	for _, change := range []func(*DirectChildGasBinding){
		func(b *DirectChildGasBinding) { b.ClaimID = uuid.New() }, func(b *DirectChildGasBinding) { b.SourceAddressID = uuid.New() },
		func(b *DirectChildGasBinding) { b.SourceDerivationIndex++ }, func(b *DirectChildGasBinding) { b.SourceExternalUserID = "other-user" },
		func(b *DirectChildGasBinding) { b.NetworkID++ }, func(b *DirectChildGasBinding) { b.TokenContract = gasPlanDestination },
		func(b *DirectChildGasBinding) { b.TokenDecimals++ }, func(b *DirectChildGasBinding) { b.AmountRaw = "6100001" },
		func(b *DirectChildGasBinding) { b.Recipient = gasPlanBase }, func(b *DirectChildGasBinding) { b.AccountID = uuid.New() },
	} {
		b := f.binding
		change(&b)
		if _, err := f.svc.ObserveDirectChildGas(context.Background(), b); !errors.Is(err, ErrGasPreparationBinding) {
			t.Fatalf("changed binding was observed: %v", err)
		}
		if _, err := f.svc.ResumeDirectChildGas(context.Background(), b); !errors.Is(err, ErrGasPreparationBinding) {
			t.Fatalf("changed binding was resumed: %v", err)
		}
		if _, err := f.svc.prepareDirectChildGas(context.Background(), b, *f.plan, f.sign); !errors.Is(err, ErrGasPreparationBinding) {
			t.Fatalf("changed binding was prepared: %v", err)
		}
	}
	if f.signs.Load() != 1 || f.sends.Load() != 1 || len(f.store.created) != 1 {
		t.Fatal("binding drift changed the committed step")
	}
}

func TestDirectChildGas_InclusionRequiresObservedChildGasAndNeverReseeds(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	f.port.GetTransactionBlockVal = 123
	result, err := f.svc.ResumeDirectChildGas(context.Background(), f.binding)
	if err != nil || result == nil || result.Confirmed || !result.Included || f.sends.Load() != 1 {
		t.Fatalf("included seed without observed gas was not held: %+v %v", result, err)
	}
	fee, _ := gasPositive(f.plan.TokenFeeWei)
	f.port.balances[f.child.Address] = fee
	result, err = f.svc.ObserveDirectChildGas(context.Background(), f.binding)
	if err != nil || !result.Confirmed || f.sends.Load() != 1 || f.signs.Load() != 1 {
		t.Fatalf("confirmed preparation was re-signed or sent: %+v %v", result, err)
	}
}

func TestDirectChildGas_OperationRetryKeepsItsCommittedChildDespiteOtherFundedSources(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	other := f.child
	other.ID = uuid.New()
	other.Address = gasPlanDestination
	other.DerivationIndex++
	f.port.tokens[other.Address] = big.NewInt(50_000_000)
	f.port.balances[other.Address] = big.NewInt(10_000_000)
	f.svc.addressRepo = &fakeAddressRepo{children: []models.Address{*f.wallet.DepositAddress, other, f.child}}
	plan, err := f.svc.PlanForOperation(context.Background(), f.binding.OperationID, f.wallet.ID, f.binding.Asset, f.binding.AmountRaw, Destination{Address: f.binding.Recipient}, f.binding.AccountID)
	if err != nil || plan.SourceAddress.ID != f.child.ID || !plan.gasPreparationExisting || plan.GasPreparation == nil {
		t.Fatalf("retry replanned another source: %+v %v", plan, err)
	}
	_, err = f.svc.PlanForOperation(context.Background(), f.binding.OperationID, f.wallet.ID, f.binding.Asset, "6100001", Destination{Address: f.binding.Recipient}, f.binding.AccountID)
	if !errors.Is(err, ErrGasPreparationBinding) {
		t.Fatalf("retry changed the amount: %v", err)
	}
}

func TestDirectChildGas_ExpiredQuoteFeeDriftAndSealingFailureNeverSend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*gasPrepFixture)
	}{
		{"expired", func(f *gasPrepFixture) { f.plan.ExpiresAt = time.Now().Add(-time.Second) }},
		{"fee_drift", func(f *gasPrepFixture) { f.port.gasPrice = big.NewInt(3) }},
		{"funder_lost_balance", func(f *gasPrepFixture) { f.port.balances[f.wallet.DepositAddress.Address] = new(big.Int) }},
		{"signing_row_insert_failed", func(f *gasPrepFixture) { f.store.createErr = errors.New("synthetic insert failure") }},
		{"seal_failed", func(f *gasPrepFixture) { f.svc.sealer = testSealer{err: errors.New("synthetic seal failure")} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGasPrepFixture(t)
			f.installSeedPort()
			tc.change(f)
			_, err := f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
			if err == nil || f.sends.Load() != 0 || len(f.store.created) != 0 {
				t.Fatalf("invalid preparation reached send: %v", err)
			}
		})
	}
}

func TestDirectChildGas_ResumeRespectsCurrentFunderReserveAndExactBytes(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	f.port.balances[f.wallet.DepositAddress.Address] = new(big.Int)
	_, err := f.svc.ResumeDirectChildGas(context.Background(), f.binding)
	if !errors.Is(err, ErrWalletNotGasReady) || f.sends.Load() != 1 || f.signs.Load() != 1 {
		t.Fatalf("resume spent unavailable funder balance: %v", err)
	}
}

func TestDirectChildGas_NullSenderNonceCannotMasqueradeAsPinnedZero(t *testing.T) {
	f := newGasPrepFixture(t)
	f.installSeedPort()
	_, _ = f.svc.prepareDirectChildGas(context.Background(), f.binding, *f.plan, f.sign)
	row := f.store.created[0]
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.RawPayload), &payload); err != nil {
		t.Fatal(err)
	}
	payload[chain.SenderNoncePayloadKey] = json.RawMessage("null")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	row.RawPayload = string(raw)
	if _, err = f.svc.ObserveDirectChildGas(context.Background(), f.binding); !errors.Is(err, ErrGasPreparationBinding) {
		t.Fatalf("null nonce observed as zero: %v", err)
	}
	if _, err = f.svc.ResumeDirectChildGas(context.Background(), f.binding); !errors.Is(err, ErrGasPreparationBinding) {
		t.Fatalf("null nonce was sent: %v", err)
	}
	if f.signs.Load() != 1 || f.sends.Load() != 1 {
		t.Fatal("null nonce caused signing or send")
	}
}

func TestDirectChildGas_NormalOperationSignsOnlySeedAndSameChildTokenPayment(t *testing.T) {
	port := newMPCGasSeedPort()
	port.gasPrice, port.nativeGasLimit, port.tokenGasLimit = big.NewInt(2), 21_000, 65_000
	fixture := newSecp256k1WalletFixture(t, port, 25)
	wallet, child := fixture.wallet, fixture.child
	account := uuid.New()
	wallet.AccountID = &account
	wallet.Status = "active"
	wallet.DepositAddress.IsActive, child.IsActive = true, true
	port.balances = map[string]*big.Int{wallet.DepositAddress.Address: big.NewInt(1_000_000), child.Address: new(big.Int)}
	port.tokens = map[string]*big.Int{wallet.DepositAddress.Address: new(big.Int), child.Address: big.NewInt(11_000_000)}
	svc := fixture.executor(port)
	network := int64(80002)
	svc.chainRepo = &fakeChainRepo{chain: &models.Chain{ID: wallet.Chain, Status: "active", AdapterType: models.AdapterTypeEVM, NetworkID: &network}}
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{*wallet.DepositAddress, child}}
	svc.registry.(*chain.Registry).RegisterToken(gasPlanToken)
	store := &memoryGasStore{fakeTxRepo: &fakeTxRepo{}}
	svc.txRepo = store
	svc.sealer = testSealer{}
	var sent []*types.SignedTx
	port.GetTransactionBlockFn = func(context.Context, string) (uint64, error) { return 123, nil }
	port.BroadcastTransactionFn = func(_ context.Context, signed *types.SignedTx) (string, error) {
		if store.commits.Load() == 0 {
			t.Fatal("normal operation sent an uncommitted preparation")
		}
		captured := *signed
		captured.RawBytes = append([]byte(nil), signed.RawBytes...)
		sent = append(sent, &captured)
		if len(sent) == 1 {
			var tx gethtypes.Transaction
			if err := tx.UnmarshalBinary(signed.RawBytes); err != nil {
				t.Fatal(err)
			}
			port.balances[child.Address] = new(big.Int).Set(tx.Value())
		}
		return signed.TxHash, nil
	}
	op := uuid.New()
	plan, err := svc.PlanForOperation(context.Background(), op, wallet.ID, gasPlanAsset, "6100000", Destination{Address: gasPlanDestination}, account)
	if err != nil || plan.GasPreparation == nil || plan.SourceAddress.ID != child.ID || len(plan.Sweeps) != 0 {
		t.Fatalf("normal plan changed the child or swept: %+v %v", plan, err)
	}
	// No receipt may be invented before the seed was sent.
	port.GetTransactionBlockFn = func(_ context.Context, hash string) (uint64, error) {
		for _, tx := range sent {
			if tx.TxHash == hash {
				return 123, nil
			}
		}
		return 0, nil
	}
	result, err := svc.ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), gasPlanDestination, child.ExternalUserID)
	if err != nil || result == nil || result.FinalWithdrawTx == nil {
		t.Fatalf("normal execution: %+v %v", result, err)
	}
	if len(sent) != 2 || len(store.created) != 2 || result.FinalWithdrawTx.FromAddress != child.Address {
		t.Fatal("normal operation was not exactly seed + original-child payment")
	}
	for i, expected := range []string{wallet.DepositAddress.Address, child.Address} {
		var tx gethtypes.Transaction
		if err := tx.UnmarshalBinary(sent[i].RawBytes); err != nil {
			t.Fatal(err)
		}
		sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(tx.ChainId()), &tx)
		if err != nil || sender.Hex() != common.HexToAddress(expected).Hex() {
			t.Fatalf("wrong signer on leg %d", i)
		}
	}
	if store.created[0].TxType != models.TxTypeGasSeed || store.created[1].TxType != models.TxTypeWithdrawal || store.created[1].IdempotencyKey == nil || *store.created[1].IdempotencyKey != op.String() {
		t.Fatal("normal operation lost its original identity or ledger legs")
	}
}
