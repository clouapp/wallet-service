package sweep

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/hdkey"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	walletsvc "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	sepoliaNetworkID    int64  = 11155111
	evmE2EDestination          = "0x000000000000000000000000000000000000dEaD"
	evmE2EChildIndex           = 25
	btcE2EChildIndex           = 25
	btcE2EDestination          = "tb1q0cdjg6jv84qulpkxrsz2glclsfmu67f4mgfj77"
	btcE2EFundingTxID          = "8f1b6c1f0f3f3d8f5b1c9b4a2e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c5b"
	btcE2EFundingSats   int64  = 15_000
	btcE2EWithdrawSats  int64  = 10_000
	evmE2EGasLimit      uint64 = 21_000
	evmE2EGasPriceWei          = "2000000000"
	secp256k1TestPubLen        = 33
)

// assignDerivedEVMAddresses gives an executor test wallet a real secp256k1 public
// key and chain code and rewrites the base and child rows to the addresses the
// production derivation produces, so the signer ownership check accepts them.
func assignDerivedEVMAddresses(t *testing.T, wallet *models.Wallet, base *models.Address, children ...*models.Address) {
	t.Helper()
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	chainCode := make([]byte, hdkey.ChainCodeSize)
	if _, err := rand.Read(chainCode); err != nil {
		t.Fatal(err)
	}
	walletKey := privateKey.PubKey().SerializeCompressed()
	wallet.MPCPublicKey = hex.EncodeToString(walletKey)
	wallet.MPCChainCode = hex.EncodeToString(chainCode)

	base.Address = mustAddress(t, wallet.Chain, false, walletKey)
	base.DerivationType = "genesis"
	for i, child := range children {
		index := uint32(i + 1)
		derived, err := hdkey.DeriveSecp256k1Child(walletKey, chainCode, index)
		if err != nil {
			t.Fatal(err)
		}
		child.Address = mustAddress(t, wallet.Chain, false, derived.PublicKey)
		child.DerivationType = bip32DerivationType
		child.DerivationIndex = int(index)
		child.WalletID = wallet.ID
	}
}

func mustAddress(t *testing.T, chainID string, testnet bool, publicKey []byte) string {
	t.Helper()
	address, err := addressing.DeriveAddressOnNetwork(chainID, testnet, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

// secp256k1KeygenOnce runs one real 2-of-2 ECDSA keygen for the whole package; the
// ceremony is slow and the tests only need a key, not a fresh one each.
var secp256k1KeygenOnce = sync.OnceValues(func() (*mpcpkg.KeygenResult, error) {
	return mpcpkg.NewTSSService().Keygen(context.Background(), mpcpkg.CurveSecp256k1)
})

// secp256k1WalletFixture is a real MPC wallet whose child comes from the production
// address service, exactly as a user deposit address is issued.
type secp256k1WalletFixture struct {
	tss    *mpcpkg.TSSService
	wallet *models.Wallet
	shareA []byte
	shareB []byte
	child  models.Address
}

func newSecp256k1WalletFixture(t *testing.T, adapter types.Chain, childIndex int) *secp256k1WalletFixture {
	t.Helper()
	keys, err := secp256k1KeygenOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.CombinedPubKey) != secp256k1TestPubLen || len(keys.ChainCode) != hdkey.ChainCodeSize {
		t.Fatalf("keygen returned a %d-byte key and %d-byte chain code", len(keys.CombinedPubKey), len(keys.ChainCode))
	}
	testnet := false
	if reporter, ok := adapter.(testnetReporter); ok {
		testnet = reporter.IsTestnet()
	}
	walletID := uuid.New()
	base := models.Address{
		ID: uuid.New(), WalletID: walletID, Chain: adapter.ID(), DerivationType: "genesis",
		Address: mustAddress(t, adapter.ID(), testnet, keys.CombinedPubKey),
	}
	wallet := &models.Wallet{
		ID:               walletID,
		Chain:            adapter.ID(),
		MPCPublicKey:     hex.EncodeToString(keys.CombinedPubKey),
		MPCCurve:         string(mpcpkg.CurveSecp256k1),
		MPCChainCode:     hex.EncodeToString(keys.ChainCode),
		DepositAddressID: &base.ID,
		DepositAddress:   &base,
	}

	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	walletRepo := &indexedWalletRepo{fakeWalletRepo: &fakeWalletRepo{wallet: wallet}, next: childIndex}
	addressService := walletsvc.NewService(walletsvc.Deps{
		Registry:  registry,
		Wallets:   walletRepo,
		Addresses: &fakeAddressRepo{},
	})
	child, err := addressService.GenerateAddress(context.Background(), walletID, "user-3119", "", "{}", "")
	if err != nil {
		t.Fatal(err)
	}
	if child.Address == base.Address || child.DerivationType != bip32DerivationType || child.DerivationIndex != childIndex {
		t.Fatalf("unexpected child row: %+v", child)
	}
	return &secp256k1WalletFixture{
		tss:    mpcpkg.NewTSSService(),
		wallet: wallet,
		shareA: append([]byte(nil), keys.ShareA...),
		shareB: append([]byte(nil), keys.ShareB...),
		child:  *child,
	}
}

func (f *secp256k1WalletFixture) executor(adapter types.Chain) *service {
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	return &service{
		registry:   registry,
		mpc:        f.tss,
		walletRepo: &fakeWalletRepo{wallet: f.wallet},
		txRepo:     &fakeTxRepo{},
		fetchShareBFn: func(context.Context, *models.Wallet) ([]byte, error) {
			return append([]byte(nil), f.shareB...), nil
		},
	}
}

func (f *secp256k1WalletFixture) credentials() SigningCredentials {
	return SigningCredentials{ShareA: append([]byte(nil), f.shareA...)}
}

// ---------------------------------------------------------------------------
// EVM: threshold signing with the BIP-32 tweak applied inside the ceremony.
// ---------------------------------------------------------------------------

// evmSigningChain finalizes and verifies with the production EVM adapter and
// records broadcasts instead of sending them.
type evmSigningChain struct {
	*mocks.MockChain
	live *chain.EVMLive
}

func (c *evmSigningChain) FinalizeMPCSignature(unsigned *types.UnsignedTx, signature, publicKey []byte) (*types.SignedTx, error) {
	return c.live.FinalizeMPCSignature(unsigned, signature, publicKey)
}

func (c *evmSigningChain) VerifySignedTransaction(unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error {
	return c.live.VerifySignedTransaction(unsigned, signed, from)
}

func evmNativeTransfer(t *testing.T, nonce uint64, to string, amount *big.Int) *types.UnsignedTx {
	t.Helper()
	gasPrice, _ := new(big.Int).SetString(evmE2EGasPriceWei, 10)
	transaction := gethtypes.NewTransaction(nonce, common.HexToAddress(to), amount, evmE2EGasLimit, gasPrice, nil)
	signer := gethtypes.LatestSignerForChainID(big.NewInt(sepoliaNetworkID))
	return &types.UnsignedTx{
		ChainID:  models.ChainETH,
		RawBytes: signer.Hash(transaction).Bytes(),
		Metadata: map[string]interface{}{
			"nonce":     nonce,
			"to":        to,
			"value":     amount.String(),
			"gas_price": evmE2EGasPriceWei,
			"gas_limit": evmE2EGasLimit,
			"chain_id":  sepoliaNetworkID,
		},
	}
}

func newEVMSigningChain(t *testing.T, broadcasts *[]*types.SignedTx) *evmSigningChain {
	t.Helper()
	mockChain := sweepMockChain(models.ChainETH, models.NativeETH)
	mockChain.GetBalanceFn = func(ctx context.Context, address string) (*types.Balance, error) {
		return &types.Balance{Address: address, Asset: models.NativeETH, Amount: big.NewInt(2_000_000_000_000_000)}, nil
	}
	mockChain.BuildTransferFn = func(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
		return evmNativeTransfer(t, 0, req.To, req.Amount), nil
	}
	mockChain.BuildSweepFn = func(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
		return []types.UnsignedTx{*evmNativeTransfer(t, 0, req.To, req.Amount)}, nil
	}
	mockChain.BroadcastTransactionFn = func(ctx context.Context, signed *types.SignedTx) (string, error) {
		*broadcasts = append(*broadcasts, signed)
		return signed.TxHash, nil
	}
	live := chain.NewEVMLive(chain.EVMConfig{ChainIDStr: models.ChainETH, NativeSymbol: models.NativeETH, NetworkID: sepoliaNetworkID})
	return &evmSigningChain{MockChain: mockChain, live: live}
}

// assertEVMSentFrom recovers the sender of a broadcast transaction with
// go-ethereum alone, the same recovery every node performs.
func assertEVMSentFrom(t *testing.T, signed *types.SignedTx, address string) {
	t.Helper()
	var transaction gethtypes.Transaction
	if err := transaction.UnmarshalBinary(signed.RawBytes); err != nil {
		t.Fatal(err)
	}
	if transaction.ChainId().Int64() != sepoliaNetworkID {
		t.Fatalf("chain id %s", transaction.ChainId())
	}
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(transaction.ChainId()), &transaction)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(sender.Hex(), address) {
		t.Fatalf("transaction recovers to %s, want %s", sender.Hex(), address)
	}
}

func TestExecutePlan_EVMChildSignsAsTheChildAddress(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	child := fixture.child
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1_000_000_000_000_000), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	if _, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), evmE2EDestination, "user-3119"); err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertEVMSentFrom(t, broadcasts[0], child.Address)
}

func TestExecutePlan_EVMBaseStillSignsAsTheWalletAddress(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	base := *fixture.wallet.DepositAddress
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1_000_000_000_000_000), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	if _, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), evmE2EDestination, "user-3119"); err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertEVMSentFrom(t, broadcasts[0], base.Address)
}

func TestConsolidate_EVMChildSweepToBaseSignsAsTheChild(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH}

	_, err := fixture.executor(adapter).broadcastLeg(context.Background(), adapter, mpcpkg.CurveSecp256k1,
		walletKeys{shareA: fixture.shareA, shareB: fixture.shareB}, fixture.wallet, plan,
		PlannedSweep{From: fixture.child, Amount: big.NewInt(1_000_000_000_000_000)}, uuid.New(),
		legBroadcastOpts{Origin: models.TxOriginManualConsolidation})
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertEVMSentFrom(t, broadcasts[0], fixture.child.Address)
}

func TestExecutePlan_EVMChildRowWithWrongIndexIsNotSigned(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	child := fixture.child
	child.DerivationIndex++
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	_, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), evmE2EDestination, "user-3119")
	if err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("want ownership error, got %v", err)
	}
	if len(broadcasts) != 0 {
		t.Fatal("nothing may be broadcast for a row the key does not own")
	}
}

func TestExecutePlan_EVMChildOfAnotherWalletIsNotSigned(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	child := fixture.child
	child.WalletID = uuid.New()
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	_, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), evmE2EDestination, "user-3119")
	if err == nil || !strings.Contains(err.Error(), "belongs to wallet") {
		t.Fatalf("want wallet mismatch error, got %v", err)
	}
	if len(broadcasts) != 0 {
		t.Fatal("nothing may be broadcast for another wallet's address")
	}
}

func TestExecutePlan_EVMChildWithoutChainCodeIsNotSigned(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	fixture.wallet.MPCChainCode = ""
	child := fixture.child
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	_, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), evmE2EDestination, "user-3119")
	if err == nil || !strings.Contains(err.Error(), "chain code") {
		t.Fatalf("want chain code error, got %v", err)
	}
	if len(broadcasts) != 0 {
		t.Fatal("nothing may be broadcast without the chain code")
	}
}

func TestExecutePlan_FailedSignedTransactionVerificationBlocksBroadcast(t *testing.T) {
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainETH, DepositAddress: &base, MPCCurve: string(mpcpkg.CurveSecp256k1)}
	assignDerivedEVMAddresses(t, wallet, &base)
	mockChain := sweepMockChain(models.ChainETH, models.NativeETH)
	mockChain.VerifySignedTransactionFn = func(*types.UnsignedTx, *types.SignedTx, string) error {
		return errors.New("recovered another sender")
	}
	svc, txRepo := newExecutorService(t, wallet, mockChain)
	source := base
	plan := &Plan{WalletID: walletID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &source}

	_, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")}, uuid.New(), evmE2EDestination, "user-1")
	if err == nil || !strings.Contains(err.Error(), "failed verification") {
		t.Fatalf("want verification error, got %v", err)
	}
	if mockChain.BroadcastTransactionCalls != 0 || len(txRepo.created) != 0 {
		t.Fatal("a transaction that failed verification must not be broadcast or recorded")
	}
}

func TestExecutePlan_BaseRowOfAnotherWalletIsNotSigned(t *testing.T) {
	walletID := uuid.New()
	base := models.Address{ID: uuid.New(), WalletID: walletID}
	wallet := &models.Wallet{ID: walletID, Chain: models.ChainETH, DepositAddress: &base, MPCCurve: string(mpcpkg.CurveSecp256k1)}
	assignDerivedEVMAddresses(t, wallet, &base)
	base.WalletID = uuid.New()
	mockChain := sweepMockChain(models.ChainETH, models.NativeETH)
	svc, _ := newExecutorService(t, wallet, mockChain)
	source := base
	plan := &Plan{WalletID: walletID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &source}

	_, err := svc.ExecutePlan(context.Background(), plan, SigningCredentials{ShareA: []byte("fake-share-a")}, uuid.New(), evmE2EDestination, "user-1")
	if err == nil || !strings.Contains(err.Error(), "belongs to wallet") {
		t.Fatalf("want wallet mismatch error, got %v", err)
	}
	if mockChain.BroadcastTransactionCalls != 0 {
		t.Fatal("nothing may be broadcast for another wallet's base row")
	}
}

// ---------------------------------------------------------------------------
// Bitcoin P2WPKH: the reconstructed wallet key plus the BIP-32 tweak.
// ---------------------------------------------------------------------------

// bitcoinSigningChain is the production testnet Bitcoin adapter backed by a fake
// Esplora that knows one confirmed UTXO; broadcasts are recorded, never sent.
type bitcoinSigningChain struct {
	*chain.BitcoinLive
	broadcasts *[]*types.SignedTx
}

func (c *bitcoinSigningChain) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	*c.broadcasts = append(*c.broadcasts, signed)
	return signed.TxHash, nil
}

// fakeEsplora serves a single confirmed UTXO for every address the test funds.
type fakeEsplora struct {
	mu     sync.Mutex
	funded map[string]int64
}

func (f *fakeEsplora) fund(address string, sats int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.funded[address] = sats
}

func (f *fakeEsplora) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const utxoSuffix = "/utxo"
	_, rest, found := strings.Cut(r.URL.Path, "/address/")
	if !found || !strings.HasSuffix(rest, utxoSuffix) {
		http.NotFound(w, r)
		return
	}
	address := strings.TrimSuffix(rest, utxoSuffix)
	f.mu.Lock()
	sats, ok := f.funded[address]
	f.mu.Unlock()
	utxos := []map[string]interface{}{}
	if ok {
		utxos = append(utxos, map[string]interface{}{
			"txid": btcE2EFundingTxID, "vout": 0, "value": sats,
			"status": map[string]interface{}{"confirmed": true, "block_height": 154745},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(utxos)
}

func newBitcoinSigningChain(t *testing.T, broadcasts *[]*types.SignedTx) (*bitcoinSigningChain, *fakeEsplora) {
	t.Helper()
	esplora := &fakeEsplora{funded: map[string]int64{}}
	server := httptest.NewServer(esplora)
	t.Cleanup(server.Close)
	live := chain.NewBitcoinLive(chain.BitcoinConfig{
		ChainIDStr:   models.ChainBTC,
		NativeSymbol: models.NativeBTC,
		// The path keeps the adapter on its Esplora REST client.
		RPCURL:         server.URL + "/mempool.space/testnet4/api",
		Network:        "bitcoin-testnet4",
		IsTestnet:      true,
		Confirmations:  1,
		FeeRateDefault: 2,
	})
	return &bitcoinSigningChain{BitcoinLive: live, broadcasts: broadcasts}, esplora
}

// assertBitcoinSpendValid checks every input the way a node evaluates a P2WPKH
// spend (OP_DUP OP_HASH160 <program> OP_EQUALVERIFY OP_CHECKSIG), with a BIP-143
// digest computed here from the spec rather than by the production code.
func assertBitcoinSpendValid(t *testing.T, signed *types.SignedTx, from string, value int64) *wire.MsgTx {
	t.Helper()
	var transaction wire.MsgTx
	if err := transaction.Deserialize(bytes.NewReader(signed.RawBytes)); err != nil {
		t.Fatal(err)
	}
	address, err := btcutil.DecodeAddress(from, &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatal(err)
	}
	program := address.ScriptAddress()
	for index, input := range transaction.TxIn {
		if len(input.Witness) != 2 {
			t.Fatalf("input %d witness has %d items", index, len(input.Witness))
		}
		signatureWithHashType, publicKeyBytes := input.Witness[0], input.Witness[1]
		if !bytes.Equal(btcutil.Hash160(publicKeyBytes), program) {
			t.Fatalf("input %d witness key does not hash to %s", index, from)
		}
		publicKey, err := btcec.ParsePubKey(publicKeyBytes)
		if err != nil {
			t.Fatal(err)
		}
		if signatureWithHashType[len(signatureWithHashType)-1] != 0x01 {
			t.Fatalf("input %d is not SIGHASH_ALL", index)
		}
		signature, err := btcecdsa.ParseDERSignature(signatureWithHashType[:len(signatureWithHashType)-1])
		if err != nil {
			t.Fatal(err)
		}
		if !signature.Verify(specBIP143Digest(&transaction, index, program, value), publicKey) {
			t.Fatalf("input %d signature does not verify as a spend from %s", index, from)
		}
	}
	return &transaction
}

// specBIP143Digest is the BIP-143 SIGHASH_ALL digest for a P2WPKH input.
func specBIP143Digest(transaction *wire.MsgTx, index int, program []byte, value int64) []byte {
	var prevouts, sequences, outputs bytes.Buffer
	for _, input := range transaction.TxIn {
		prevouts.Write(input.PreviousOutPoint.Hash[:])
		_ = binary.Write(&prevouts, binary.LittleEndian, input.PreviousOutPoint.Index)
		_ = binary.Write(&sequences, binary.LittleEndian, input.Sequence)
	}
	for _, output := range transaction.TxOut {
		_ = binary.Write(&outputs, binary.LittleEndian, output.Value)
		_ = wire.WriteVarBytes(&outputs, 0, output.PkScript)
	}
	scriptCode := append(append([]byte{0x19, 0x76, 0xa9, 0x14}, program...), 0x88, 0xac)

	var preimage bytes.Buffer
	_ = binary.Write(&preimage, binary.LittleEndian, transaction.Version)
	preimage.Write(chainhash.DoubleHashB(prevouts.Bytes()))
	preimage.Write(chainhash.DoubleHashB(sequences.Bytes()))
	spent := transaction.TxIn[index]
	preimage.Write(spent.PreviousOutPoint.Hash[:])
	_ = binary.Write(&preimage, binary.LittleEndian, spent.PreviousOutPoint.Index)
	preimage.Write(scriptCode)
	_ = binary.Write(&preimage, binary.LittleEndian, value)
	_ = binary.Write(&preimage, binary.LittleEndian, spent.Sequence)
	preimage.Write(chainhash.DoubleHashB(outputs.Bytes()))
	_ = binary.Write(&preimage, binary.LittleEndian, transaction.LockTime)
	_ = binary.Write(&preimage, binary.LittleEndian, uint32(0x01))
	return chainhash.DoubleHashB(preimage.Bytes())
}

func TestExecutePlan_BitcoinChildSpendsAsTheChildAddress(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, esplora := newBitcoinSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, btcE2EChildIndex)
	child := fixture.child
	if !strings.HasPrefix(child.Address, addressing.BtcHRPTestnet+"1") {
		t.Fatalf("testnet child address %s", child.Address)
	}
	esplora.fund(child.Address, btcE2EFundingSats)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainBTC, Asset: models.NativeBTC,
		Amount: big.NewInt(btcE2EWithdrawSats), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	if _, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), btcE2EDestination, "user-3119"); err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	transaction := assertBitcoinSpendValid(t, broadcasts[0], child.Address, btcE2EFundingSats)
	if transaction.TxOut[0].Value != btcE2EWithdrawSats {
		t.Fatalf("payment output %d sats", transaction.TxOut[0].Value)
	}
}

func TestExecutePlan_BitcoinBaseStillSpendsAsTheWalletAddress(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, esplora := newBitcoinSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, btcE2EChildIndex)
	base := *fixture.wallet.DepositAddress
	esplora.fund(base.Address, btcE2EFundingSats)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainBTC, Asset: models.NativeBTC,
		Amount: big.NewInt(btcE2EWithdrawSats), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	if _, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), btcE2EDestination, "user-3119"); err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
	assertBitcoinSpendValid(t, broadcasts[0], base.Address, btcE2EFundingSats)
}

func TestExecutePlan_BitcoinChildRowWithWrongIndexIsNotSigned(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, esplora := newBitcoinSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, btcE2EChildIndex)
	child := fixture.child
	child.DerivationIndex--
	esplora.fund(child.Address, btcE2EFundingSats)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainBTC, Asset: models.NativeBTC,
		Amount: big.NewInt(btcE2EWithdrawSats), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	_, err := fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), btcE2EDestination, "user-3119")
	if err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("want ownership error, got %v", err)
	}
	if len(broadcasts) != 0 {
		t.Fatal("nothing may be broadcast for a row the key does not own")
	}
}

func TestExecutePlan_BitcoinSharesOfAnotherWalletAreRejected(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, esplora := newBitcoinSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, btcE2EChildIndex)
	impostor, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	fixture.wallet.MPCPublicKey = hex.EncodeToString(impostor.PubKey().SerializeCompressed())
	base := *fixture.wallet.DepositAddress
	base.Address = mustAddress(t, models.ChainBTC, true, impostor.PubKey().SerializeCompressed())
	fixture.wallet.DepositAddress = &base
	esplora.fund(base.Address, btcE2EFundingSats)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainBTC, Asset: models.NativeBTC,
		Amount: big.NewInt(btcE2EWithdrawSats), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	_, err = fixture.executor(adapter).ExecutePlan(context.Background(), plan, fixture.credentials(), uuid.New(), btcE2EDestination, "user-3119")
	if err == nil || !strings.Contains(err.Error(), "does not match the expected public key") {
		t.Fatalf("want key mismatch error, got %v", err)
	}
	if len(broadcasts) != 0 {
		t.Fatal("nothing may be broadcast with shares of another wallet")
	}
}
