package evmcall

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	testChainID    = int64(11155111)
	testFrom       = "0x2981d7059fa276a44d5e1446487b96f42444df12"
	testInbox      = "0xaAe29B0366299461418F5324a79Afc425BE5ae21"
	testSignedHash = "0x350e68897f862f6453255c6e34f63a8b4aed1cdf887fff62b36958f17f04977b"
	testPassphrase = "a-passphrase-of-length"
	testTag        = "bridge-test-1"
	testEstimate   = uint64(93_065)
	testGasPrice   = int64(1_000_000_000)
)

var testDepositEth = []byte{0x43, 0x93, 0x70, 0xb1}

type fakeRPC struct {
	chainID       int64
	code          []byte
	latestNonce   uint64
	pendingNonces []uint64
	balance       *big.Int
	gasPrice      *big.Int
	callErr       error
	estimate      uint64
	estimateErr   error
	sendHash      string
	sendErr       error
	known         bool
	receipts      []*Receipt

	codeCalls, sendCalls, receiptCalls int
	lastCall                           CallMsg
}

func newFakeRPC() *fakeRPC {
	return &fakeRPC{
		chainID: testChainID, code: []byte{0x60, 0x80}, balance: big.NewInt(50_000_000_000_000_000),
		gasPrice: big.NewInt(testGasPrice), estimate: testEstimate, sendHash: testSignedHash,
		receipts: []*Receipt{{Status: statusOK, BlockNumber: 11832728, GasUsed: 91_174, EffectiveGasPrice: big.NewInt(2 * testGasPrice)}},
	}
}

func (f *fakeRPC) ChainID(context.Context) (int64, error) { return f.chainID, nil }
func (f *fakeRPC) Code(context.Context, string) ([]byte, error) {
	f.codeCalls++
	return f.code, nil
}
func (f *fakeRPC) Nonce(_ context.Context, _ string, block string) (uint64, error) {
	if block == blockLatest || len(f.pendingNonces) == 0 {
		return f.latestNonce, nil
	}
	next := f.pendingNonces[0]
	if len(f.pendingNonces) > 1 {
		f.pendingNonces = f.pendingNonces[1:]
	}
	return next, nil
}
func (f *fakeRPC) Balance(context.Context, string) (*big.Int, error) { return f.balance, nil }
func (f *fakeRPC) GasPrice(context.Context) (*big.Int, error)        { return f.gasPrice, nil }
func (f *fakeRPC) Call(_ context.Context, msg CallMsg) ([]byte, error) {
	f.lastCall = msg
	return []byte{0x21, 0x96, 0x1a}, f.callErr
}
func (f *fakeRPC) EstimateGas(context.Context, CallMsg) (uint64, error) {
	return f.estimate, f.estimateErr
}
func (f *fakeRPC) SendRawTransaction(context.Context, []byte) (string, error) {
	f.sendCalls++
	return f.sendHash, f.sendErr
}
func (f *fakeRPC) TransactionKnown(context.Context, string) (bool, error) { return f.known, nil }
func (f *fakeRPC) Receipt(context.Context, string) (*Receipt, error) {
	f.receiptCalls++
	if len(f.receipts) == 0 {
		return nil, nil
	}
	next := f.receipts[0]
	f.receipts = f.receipts[1:]
	return next, nil
}

type fakeWallets struct{ wallet *models.Wallet }

func (f fakeWallets) FindByID(id uuid.UUID) (*models.Wallet, error) {
	if f.wallet == nil || f.wallet.ID != id {
		return nil, errors.New("not found")
	}
	return f.wallet, nil
}

type fakeSigner struct {
	calls     int
	chainID   interface{}
	passwords []string
}

func (f *fakeSigner) PreflightEVMCall(_ context.Context, _ uuid.UUID, passphrase string, adapter types.Chain, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
	f.calls++
	f.chainID = unsigned.Metadata["chain_id"]
	f.passwords = append(f.passwords, passphrase)
	return &types.SignedTx{ChainID: adapter.ID(), RawBytes: []byte{0x01, 0x02}, TxHash: testSignedHash}, nil
}

type testHarness struct {
	rpc     *fakeRPC
	signer  *fakeSigner
	wallet  *models.Wallet
	service *Service
	dir     string
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	walletID := uuid.New()
	wallet := &models.Wallet{
		ID: walletID, Chain: models.ChainArbitrum, MPCCurve: string(mpcpkg.CurveSecp256k1),
		DepositAddress: &models.Address{WalletID: walletID, Address: testFrom},
	}
	h := &testHarness{rpc: newFakeRPC(), signer: &fakeSigner{}, wallet: wallet, dir: t.TempDir()}
	h.service = h.newService(t)
	return h
}

func (h *testHarness) newService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(Dependencies{RPC: h.rpc, Wallets: fakeWallets{wallet: h.wallet}, Signer: h.signer, Claimer: FileClaimer{Dir: h.dir}})
	if err != nil {
		t.Fatal(err)
	}
	service.sleep = func(context.Context, time.Duration) error { return nil }
	service.receiptPoll = time.Millisecond
	return service
}

func (h *testHarness) request() Request {
	return Request{WalletID: h.wallet.ID, ChainID: testChainID, To: testInbox, Data: testDepositEth,
		Value: big.NewInt(30_000_000_000_000_000), Tag: testTag}
}

func TestSimulate_PlansWithoutSigningOrSending(t *testing.T) {
	h := newHarness(t)
	plan, err := h.service.Simulate(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if h.signer.calls != 0 || h.rpc.sendCalls != 0 {
		t.Fatalf("dry run signed %d and sent %d", h.signer.calls, h.rpc.sendCalls)
	}
	wantGasLimit := testEstimate * gasLimitMarginPercent / percentDenominator
	if plan.GasLimit != wantGasLimit || plan.GasPriceWei != "2000000000" || plan.Nonce != 0 || plan.From != testFrom {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if plan.Value != "0.03 ETH" || plan.Data != "0x439370b1" || plan.CallResult != "0x21961a" {
		t.Fatalf("value %q data %q call %q", plan.Value, plan.Data, plan.CallResult)
	}
	if h.rpc.lastCall.From != testFrom || h.rpc.lastCall.Value.Cmp(big.NewInt(30_000_000_000_000_000)) != 0 {
		t.Fatalf("simulated %+v", h.rpc.lastCall)
	}
	if entries, _ := os.ReadDir(h.dir); len(entries) != 0 {
		t.Fatalf("dry run wrote %d claim files", len(entries))
	}
}

func TestSimulate_HonorsAGasLimitThatCoversTheEstimate(t *testing.T) {
	h := newHarness(t)
	request := h.request()
	request.GasLimit = 150_000
	plan, err := h.service.Simulate(context.Background(), request)
	if err != nil || plan.GasLimit != 150_000 {
		t.Fatalf("plan %+v err %v", plan, err)
	}
}

func TestSimulate_PlainTransferNeedsNoBytecode(t *testing.T) {
	h := newHarness(t)
	h.rpc.code = nil
	request := h.request()
	request.Data, request.To, request.Value = nil, testFrom, new(big.Int)
	if _, err := h.service.Simulate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if h.rpc.codeCalls != 0 {
		t.Fatalf("checked bytecode %d times for a call without data", h.rpc.codeCalls)
	}
}

func TestSimulate_RefusesUnsafeCalls(t *testing.T) {
	cases := map[string]func(h *testHarness, r *Request){
		"mainnet chain id":        func(_ *testHarness, r *Request) { r.ChainID = 1 },
		"bsc mainnet":             func(_ *testHarness, r *Request) { r.ChainID = 56 },
		"rpc on another chain":    func(h *testHarness, _ *Request) { h.rpc.chainID = 1 },
		"calldata to no bytecode": func(h *testHarness, _ *Request) { h.rpc.code = nil },
		"pending transaction":     func(h *testHarness, _ *Request) { h.rpc.pendingNonces = []uint64{1} },
		"eth_call reverts":        func(h *testHarness, _ *Request) { h.rpc.callErr = errors.New("execution reverted") },
		"estimate fails":          func(h *testHarness, _ *Request) { h.rpc.estimateErr = errors.New("execution reverted") },
		"zero estimate":           func(h *testHarness, _ *Request) { h.rpc.estimate = 0 },
		"insufficient balance":    func(h *testHarness, _ *Request) { h.rpc.balance = big.NewInt(1) },
		"gas limit below estimate": func(_ *testHarness, r *Request) {
			r.GasLimit = testEstimate - 1
		},
		"gas price above cap":  func(h *testHarness, _ *Request) { h.rpc.gasPrice = big.NewInt(MaxGasPriceWei) },
		"ed25519 wallet":       func(h *testHarness, _ *Request) { h.wallet.MPCCurve = string(mpcpkg.CurveEd25519) },
		"non-EVM base address": func(h *testHarness, _ *Request) { h.wallet.DepositAddress.Address = "tb1qexample" },
		"unknown wallet":       func(_ *testHarness, r *Request) { r.WalletID = uuid.New() },
	}
	for name, mutate := range cases {
		h := newHarness(t)
		request := h.request()
		mutate(h, &request)
		if _, err := h.service.Simulate(context.Background(), request); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if h.rpc.sendCalls != 0 || h.signer.calls != 0 {
			t.Errorf("%s: signed %d, sent %d", name, h.signer.calls, h.rpc.sendCalls)
		}
	}
}

func TestBroadcast_SignsForTheRequestedChainAndSendsOnce(t *testing.T) {
	h := newHarness(t)
	result, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if h.rpc.sendCalls != 1 || h.signer.calls != 1 {
		t.Fatalf("sent %d, signed %d", h.rpc.sendCalls, h.signer.calls)
	}
	if h.signer.chainID != testChainID {
		t.Fatalf("signed for chain %v", h.signer.chainID)
	}
	if result.Outcome != OutcomeReceiptSuccess || result.TxHash != testSignedHash || result.Receipt.BlockNumber != 11832728 {
		t.Fatalf("unexpected result %+v", result)
	}
	if result.FeeWei != new(big.Int).Mul(big.NewInt(91_174), big.NewInt(2*testGasPrice)).String() {
		t.Fatalf("fee %s", result.FeeWei)
	}
	assertFileHolds(t, result.ClaimPath, testSignedHash)
	assertFileHolds(t, result.ResultPath, OutcomeReceiptSuccess)
	assertFileLacks(t, result.ResultPath, testPassphrase)
}

func TestBroadcast_ASecondRunWithTheSameTagNeverSends(t *testing.T) {
	h := newHarness(t)
	if _, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase); err != nil {
		t.Fatal(err)
	}
	h.rpc.receipts = []*Receipt{{Status: statusOK, BlockNumber: 1, GasUsed: 1}}
	_, err := h.newService(t).Broadcast(context.Background(), h.request(), testPassphrase)
	if !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second broadcast: %v", err)
	}
	if h.rpc.sendCalls != 1 {
		t.Fatalf("sent %d times", h.rpc.sendCalls)
	}
}

func TestBroadcast_ASendErrorIsNeverRetried(t *testing.T) {
	h := newHarness(t)
	h.rpc.sendErr = errors.New("insufficient funds for gas")
	result, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if !errors.Is(err, ErrNotSent) {
		t.Fatalf("err %v", err)
	}
	if h.rpc.sendCalls != 1 || h.rpc.receiptCalls != 0 || result.Outcome != OutcomeSendFailed || result.TxHash != "" {
		t.Fatalf("sent %d, receipts %d, result %+v", h.rpc.sendCalls, h.rpc.receiptCalls, result)
	}
	assertFileHolds(t, result.ResultPath, "insufficient funds for gas")
}

func TestBroadcast_ASendErrorForAKnownTransactionStillWaitsForTheReceipt(t *testing.T) {
	h := newHarness(t)
	h.rpc.sendErr, h.rpc.known = errors.New("already known"), true
	result, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if h.rpc.sendCalls != 1 || result.TxHash != testSignedHash || result.Outcome != OutcomeReceiptSuccess {
		t.Fatalf("sent %d, result %+v", h.rpc.sendCalls, result)
	}
}

func TestBroadcast_ANonceThatMovedAfterSigningSendsNothing(t *testing.T) {
	h := newHarness(t)
	h.rpc.pendingNonces = []uint64{0, 1}
	result, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if !errors.Is(err, ErrNotSent) || h.rpc.sendCalls != 0 {
		t.Fatalf("err %v, sent %d", err, h.rpc.sendCalls)
	}
	assertFileHolds(t, result.ClaimPath, testTag)
}

func TestBroadcast_AnUnexpectedHashStopsBeforeWaiting(t *testing.T) {
	h := newHarness(t)
	h.rpc.sendHash = "0x" + strings.Repeat("ab", 32)
	result, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if err == nil || result.Outcome != OutcomeHashMismatch || h.rpc.receiptCalls != 0 {
		t.Fatalf("err %v result %+v receipts %d", err, result, h.rpc.receiptCalls)
	}
}

func TestBroadcast_ReportsRevertedAndMissingReceipts(t *testing.T) {
	h := newHarness(t)
	h.rpc.receipts = []*Receipt{{Status: "0x0", BlockNumber: 7, GasUsed: 50_000}}
	result, err := h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if err != nil || result.Outcome != OutcomeReceiptReverted {
		t.Fatalf("err %v outcome %q", err, result.Outcome)
	}

	h = newHarness(t)
	h.rpc.receipts = nil
	h.service.receiptTimeout = 0
	result, err = h.service.Broadcast(context.Background(), h.request(), testPassphrase)
	if err != nil || result.Outcome != OutcomeNoReceiptYet || h.rpc.sendCalls != 1 {
		t.Fatalf("err %v outcome %q sent %d", err, result.Outcome, h.rpc.sendCalls)
	}
}

func TestBroadcast_RefusesBeforeTouchingTheNode(t *testing.T) {
	cases := map[string]func(r *Request) string{
		"no tag":           func(r *Request) string { r.Tag = ""; return testPassphrase },
		"bad tag":          func(r *Request) string { r.Tag = "Bad Tag"; return testPassphrase },
		"short passphrase": func(*Request) string { return "short" },
		"mainnet":          func(r *Request) string { r.ChainID = 42161; return testPassphrase },
	}
	for name, mutate := range cases {
		h := newHarness(t)
		request := h.request()
		passphrase := mutate(&request)
		if _, err := h.service.Broadcast(context.Background(), request, passphrase); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if h.rpc.sendCalls != 0 || h.signer.calls != 0 || h.rpc.codeCalls != 0 {
			t.Errorf("%s: touched the node or signer", name)
		}
	}
}

func TestNewService_RequiresRPCAndWallets(t *testing.T) {
	if _, err := NewService(Dependencies{Wallets: fakeWallets{}}); err == nil {
		t.Error("no rpc: expected an error")
	}
	if _, err := NewService(Dependencies{RPC: newFakeRPC()}); err == nil {
		t.Error("no wallets: expected an error")
	}
	service, err := NewService(Dependencies{RPC: newFakeRPC(), Wallets: fakeWallets{}})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{WalletID: uuid.New(), ChainID: testChainID, To: testInbox, Value: new(big.Int), Tag: testTag}
	if _, err := service.Broadcast(context.Background(), request, testPassphrase); err == nil {
		t.Error("broadcast without signer or claimer: expected an error")
	}
}

func assertFileHolds(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), want) {
		t.Fatalf("%s lacks %q", filepath.Base(path), want)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("%s is not JSON: %v", filepath.Base(path), err)
	}
}

func assertFileLacks(t *testing.T, path, unwanted string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), unwanted) {
		t.Fatalf("%s contains %q", filepath.Base(path), unwanted)
	}
}
