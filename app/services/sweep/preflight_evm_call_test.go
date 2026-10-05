package sweep

import (
	"context"
	"math/big"
	"strings"
	"testing"

	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// evmCallTestOtherNetworkID differs from the fixture adapter's network
// (sepoliaNetworkID), so the signature proves the call's own chain id is used.
const (
	evmCallTestOtherNetworkID = int64(421614)
	evmCallTestInbox          = "0xaAe29B0366299461418F5324a79Afc425BE5ae21"
)

func evmCallTestUnsigned(t *testing.T, chainID string, networkID int64) (*evmchain.EVMLive, *types.UnsignedTx) {
	t.Helper()
	adapter := evmchain.NewEVMLive(evmchain.EVMConfig{ChainIDStr: chainID, NetworkID: networkID})
	unsigned, err := adapter.BuildCall(chain.EVMCall{
		Nonce: 0, To: evmCallTestInbox, Value: big.NewInt(30_000_000_000_000_000),
		Data: []byte{0x43, 0x93, 0x70, 0xb1}, GasLimit: 120_000, GasPrice: big.NewInt(2_000_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, unsigned
}

func TestPreflightEVMCall_SignsForAnotherNetworkWithTheBaseKey(t *testing.T) {
	var broadcasts []*types.SignedTx
	walletAdapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, walletAdapter, evmE2EChildIndex)
	svc, txRepo := preflightService(t, fixture, walletAdapter)
	callAdapter, unsigned := evmCallTestUnsigned(t, models.ChainETH, evmCallTestOtherNetworkID)

	signed, err := svc.PreflightEVMCall(context.Background(), fixture.wallet.ID, preflightTestPassphrase, callAdapter, unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 0 || len(txRepo.created) != 0 {
		t.Fatalf("preflight broadcast %d txs and persisted %d rows", len(broadcasts), len(txRepo.created))
	}
	var transaction gethtypes.Transaction
	if err := transaction.UnmarshalBinary(signed.RawBytes); err != nil {
		t.Fatal(err)
	}
	if transaction.ChainId().Int64() != evmCallTestOtherNetworkID {
		t.Fatalf("chain id %s, want %d", transaction.ChainId(), evmCallTestOtherNetworkID)
	}
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(big.NewInt(evmCallTestOtherNetworkID)), &transaction)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(sender.Hex(), fixture.wallet.DepositAddress.Address) {
		t.Fatalf("signed by %s, want the base %s", sender.Hex(), fixture.wallet.DepositAddress.Address)
	}
	if transaction.Hash().Hex() != signed.TxHash {
		t.Fatalf("tx hash %q, decoded %s", signed.TxHash, transaction.Hash().Hex())
	}
}

func TestPreflightEVMCall_RefusesWhatItCannotSign(t *testing.T) {
	var broadcasts []*types.SignedTx
	walletAdapter := newEVMSigningChain(t, &broadcasts)
	fixture := newSecp256k1WalletFixture(t, walletAdapter, evmE2EChildIndex)
	svc, _ := preflightService(t, fixture, walletAdapter)
	callAdapter, unsigned := evmCallTestUnsigned(t, models.ChainETH, evmCallTestOtherNetworkID)
	otherChainAdapter, otherChainCall := evmCallTestUnsigned(t, models.ChainPolygon, evmCallTestOtherNetworkID)
	ctx := context.Background()

	if _, err := svc.PreflightEVMCall(ctx, fixture.wallet.ID, "not-the-passphrase-01", callAdapter, unsigned); err == nil {
		t.Error("wrong passphrase: expected an error")
	}
	if _, err := svc.PreflightEVMCall(ctx, fixture.wallet.ID, preflightTestPassphrase, otherChainAdapter, otherChainCall); err == nil {
		t.Error("call built for another chain than the wallet: expected an error")
	}
	if _, err := svc.PreflightEVMCall(ctx, fixture.wallet.ID, preflightTestPassphrase, otherChainAdapter, unsigned); err == nil {
		t.Error("adapter and call on different chains: expected an error")
	}
	if _, err := svc.PreflightEVMCall(ctx, fixture.wallet.ID, preflightTestPassphrase, nil, unsigned); err == nil {
		t.Error("nil adapter: expected an error")
	}
	if _, err := svc.PreflightEVMCall(ctx, fixture.wallet.ID, preflightTestPassphrase, callAdapter, &types.UnsignedTx{ChainID: models.ChainETH}); err == nil {
		t.Error("unbuilt call: expected an error")
	}
	svc.walletRepo = &fakeWalletRepo{}
	if _, err := svc.PreflightEVMCall(ctx, uuid.New(), preflightTestPassphrase, callAdapter, unsigned); err == nil {
		t.Error("unknown wallet: expected an error")
	}
	if len(broadcasts) != 0 {
		t.Fatalf("broadcasts %d", len(broadcasts))
	}
}
