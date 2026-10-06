package evm

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	verifySepoliaID   int64  = 11155111
	verifyDestination        = "0x000000000000000000000000000000000000dEaD"
	verifyGasPrice           = "2000000000"
	verifyGasLimit    uint64 = 21000
	verifyValueWei           = "1000000000000000"
	verifyNonce       uint64 = 7
)

func verifyEVMUnsigned(value string) *types.UnsignedTx {
	return &types.UnsignedTx{ChainID: models.ChainETH, Metadata: map[string]interface{}{
		"nonce": verifyNonce, "to": verifyDestination, "value": value,
		"gas_price": verifyGasPrice, "gas_limit": verifyGasLimit, "chain_id": verifySepoliaID,
	}}
}

func signEVMForVerify(t *testing.T, adapter *EVMLive, unsigned *types.UnsignedTx) (*types.SignedTx, string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	transaction, signer, err := adapter.transactionFromUnsigned(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	signedTransaction, err := gethtypes.SignTx(transaction, signer, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signedTransaction.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return &types.SignedTx{ChainID: models.ChainETH, RawBytes: raw}, crypto.PubkeyToAddress(key.PublicKey).Hex()
}

func TestEVM_VerifySignedTransaction_AcceptsTheSender(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NetworkID: verifySepoliaID})
	unsigned := verifyEVMUnsigned(verifyValueWei)
	signed, from := signEVMForVerify(t, adapter, unsigned)
	if err := adapter.VerifySignedTransaction(unsigned, signed, strings.ToLower(from)); err != nil {
		t.Fatal(err)
	}
}

func TestEVM_VerifySignedTransaction_RejectsAnotherSender(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NetworkID: verifySepoliaID})
	unsigned := verifyEVMUnsigned(verifyValueWei)
	signed, _ := signEVMForVerify(t, adapter, unsigned)
	err := adapter.VerifySignedTransaction(unsigned, signed, common.HexToAddress(verifyDestination).Hex())
	if err == nil || !strings.Contains(err.Error(), "signed by") {
		t.Fatalf("want sender mismatch, got %v", err)
	}
}

func TestEVM_VerifySignedTransaction_RejectsADifferentTransaction(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NetworkID: verifySepoliaID})
	signed, from := signEVMForVerify(t, adapter, verifyEVMUnsigned(verifyValueWei))
	other := verifyEVMUnsigned(new(big.Int).Add(mustBig(verifyValueWei), big.NewInt(1)).String())
	err := adapter.VerifySignedTransaction(other, signed, from)
	if err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("want body mismatch, got %v", err)
	}
}

func TestEVM_VerifySignedTransaction_RejectsGarbage(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NetworkID: verifySepoliaID})
	unsigned := verifyEVMUnsigned(verifyValueWei)
	if err := adapter.VerifySignedTransaction(unsigned, &types.SignedTx{RawBytes: []byte{0x01, 0x02}}, verifyDestination); err == nil {
		t.Fatal("undecodable bytes must be refused")
	}
	if err := adapter.VerifySignedTransaction(unsigned, nil, verifyDestination); err == nil {
		t.Fatal("a nil signed transaction must be refused")
	}
}

func mustBig(value string) *big.Int {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid integer " + value)
	}
	return parsed
}
