package sweep

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/pkg/types"
)

const preflightTestPassphrase = "preflight-passphrase-0001"

// preflightService is the fixture's executor with share A sealed under the test
// passphrase, the way a wallet row stores it.
func preflightService(t *testing.T, fixture *secp256k1WalletFixture, adapter types.Chain) (*service, *fakeTxRepo) {
	t.Helper()
	sealed, err := mpcpkg.EncryptShare(fixture.shareA, preflightTestPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	fixture.wallet.MPCCustomerShare = hex.EncodeToString(sealed.Ciphertext)
	fixture.wallet.MPCShareIV = hex.EncodeToString(sealed.IV)
	fixture.wallet.MPCShareSalt = hex.EncodeToString(sealed.Salt)
	svc := fixture.executor(adapter)
	txRepo := &fakeTxRepo{}
	svc.txRepo = txRepo
	return svc, txRepo
}

func TestPreflightWithdrawal_EVMBaseIsSignedVerifiedAndNotBroadcast(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	svc, txRepo := preflightService(t, fixture, adapter)
	base := *fixture.wallet.DepositAddress
	amount := big.NewInt(1_000_000_000_000_000)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: amount, Strategy: StrategyDirectFromBase, SourceAddress: &base}

	result, err := svc.PreflightWithdrawal(context.Background(), plan, preflightTestPassphrase, evmE2EDestination)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 0 || len(txRepo.created) != 0 {
		t.Fatalf("preflight broadcast %d txs and persisted %d rows", len(broadcasts), len(txRepo.created))
	}
	if len(result.Transactions) != 1 {
		t.Fatalf("transactions %d", len(result.Transactions))
	}
	signed := result.Transactions[0]
	if signed.Role != preflightRoleWithdrawal || signed.From != base.Address || signed.To != evmE2EDestination || signed.Amount.Cmp(amount) != 0 {
		t.Fatalf("unexpected preflight tx %+v", signed)
	}
	if !strings.HasPrefix(signed.TxHash, "0x") || len(signed.TxHash) != 66 {
		t.Fatalf("tx hash %q", signed.TxHash)
	}
}

func TestPreflightWithdrawal_EVMChildIsSignedAsTheChild(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	svc, _ := preflightService(t, fixture, adapter)
	child := fixture.child
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1_000_000_000_000_000), Strategy: StrategyDirectFromChild, SourceAddress: &child}

	result, err := svc.PreflightWithdrawal(context.Background(), plan, preflightTestPassphrase, evmE2EDestination)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 0 || result.Transactions[0].From != child.Address {
		t.Fatalf("broadcasts %d, from %s", len(broadcasts), result.Transactions[0].From)
	}
}

func TestPreflightWithdrawal_WrongPassphraseSignsNothing(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	svc, _ := preflightService(t, fixture, adapter)
	base := *fixture.wallet.DepositAddress
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainETH, Asset: models.NativeETH,
		Amount: big.NewInt(1), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	if _, err := svc.PreflightWithdrawal(context.Background(), plan, "not-the-passphrase-01", evmE2EDestination); err == nil {
		t.Fatal("expected an invalid passphrase error")
	}
	if adapter.BuildTransferCalls != 0 || len(broadcasts) != 0 {
		t.Fatalf("built %d transfers and broadcast %d", adapter.BuildTransferCalls, len(broadcasts))
	}
}

func TestPreflightWithdrawal_RefusesPlansItCannotSignUpFront(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	svc, _ := preflightService(t, fixture, adapter)
	cases := map[string]*Plan{
		"multi sweep":  {WalletID: fixture.wallet.ID, Chain: models.ChainETH, Strategy: StrategyMultiSweep},
		"insufficient": {WalletID: fixture.wallet.ID, Chain: models.ChainETH, Strategy: StrategyInsufficient},
		"no source":    {WalletID: fixture.wallet.ID, Chain: models.ChainETH, Strategy: StrategyDirectFromBase},
		"other chain": {WalletID: fixture.wallet.ID, Chain: models.ChainBTC, Strategy: StrategyDirectFromBase,
			Amount: big.NewInt(1), SourceAddress: fixture.wallet.DepositAddress},
	}
	for name, plan := range cases {
		if _, err := svc.PreflightWithdrawal(context.Background(), plan, preflightTestPassphrase, evmE2EDestination); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := svc.PreflightWithdrawal(context.Background(), nil, preflightTestPassphrase, evmE2EDestination); err == nil {
		t.Error("nil plan: expected an error")
	}
	if len(broadcasts) != 0 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
}

func TestPreflightConsolidation_EVMChildSweepIsSignedAsTheChild(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	svc, txRepo := preflightService(t, fixture, adapter)
	svc.chainRepo = &fakeChainRepo{chain: &models.Chain{ID: models.ChainETH, AdapterType: models.AdapterTypeEVM}}
	svc.addressRepo = &fakeAddressRepo{children: []models.Address{*fixture.wallet.DepositAddress, fixture.child}}

	result, err := svc.PreflightConsolidation(context.Background(), fixture.wallet.ID, models.NativeETH, preflightTestPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 0 || len(txRepo.created) != 0 {
		t.Fatalf("preflight broadcast %d txs and persisted %d rows", len(broadcasts), len(txRepo.created))
	}
	if result.Strategy != StrategyMultiSweep || len(result.Transactions) != 1 {
		t.Fatalf("unexpected preflight %+v", result)
	}
	sweepTx := result.Transactions[0]
	if sweepTx.Role != preflightRoleSweep || sweepTx.From != fixture.child.Address || sweepTx.To != fixture.wallet.DepositAddress.Address {
		t.Fatalf("unexpected sweep %+v", sweepTx)
	}
	if sweepTx.Amount == nil || sweepTx.Amount.Sign() <= 0 {
		t.Fatalf("sweep amount %v", sweepTx.Amount)
	}
}

func TestPreflightConsolidation_UnknownWalletFails(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, evmE2EChildIndex)
	svc, _ := preflightService(t, fixture, adapter)
	svc.walletRepo = &fakeWalletRepo{}

	if _, err := svc.PreflightConsolidation(context.Background(), uuid.New(), models.NativeETH, preflightTestPassphrase); err == nil {
		t.Fatal("expected an error for a wallet that does not exist")
	}
}

func TestPreflightWithdrawal_BitcoinBaseSpendIsSignedAndNotBroadcast(t *testing.T) {
	var broadcasts []*types.SignedTx
	adapter, esplora := newBitcoinSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, adapter, btcE2EChildIndex)
	svc, txRepo := preflightService(t, fixture, adapter)
	base := *fixture.wallet.DepositAddress
	esplora.fund(base.Address, btcE2EFundingSats)
	plan := &Plan{WalletID: fixture.wallet.ID, Chain: models.ChainBTC, Asset: models.NativeBTC,
		Amount: big.NewInt(btcE2EWithdrawSats), Strategy: StrategyDirectFromBase, SourceAddress: &base}

	result, err := svc.PreflightWithdrawal(context.Background(), plan, preflightTestPassphrase, btcE2EDestination)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 0 || len(txRepo.created) != 0 {
		t.Fatalf("preflight broadcast %d txs and persisted %d rows", len(broadcasts), len(txRepo.created))
	}
	if txid := result.Transactions[0].TxHash; len(txid) != 64 {
		t.Fatalf("txid %q", txid)
	}
}
