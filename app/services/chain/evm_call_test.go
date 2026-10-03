package chain

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	evmCallTestNetworkID = int64(11155111)
	evmCallTestInbox     = "0xaAe29B0366299461418F5324a79Afc425BE5ae21"
	evmCallTestGasLimit  = uint64(120_000)
)

var evmCallTestDepositEth = []byte{0x43, 0x93, 0x70, 0xb1}

func evmCallTestCall() EVMCall {
	return EVMCall{
		Nonce:    3,
		To:       evmCallTestInbox,
		Value:    big.NewInt(30_000_000_000_000_000),
		Data:     evmCallTestDepositEth,
		GasLimit: evmCallTestGasLimit,
		GasPrice: big.NewInt(2_000_000_000),
	}
}

func TestBuildCall_SigningHashIsTheEIP155HashOfTheCall(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "arbitrum", NetworkID: evmCallTestNetworkID})
	call := evmCallTestCall()

	unsigned, err := adapter.BuildCall(call)
	if err != nil {
		t.Fatal(err)
	}
	expected := gethtypes.NewTransaction(call.Nonce, common.HexToAddress(call.To), call.Value, call.GasLimit, call.GasPrice, call.Data)
	hash := gethtypes.LatestSignerForChainID(big.NewInt(evmCallTestNetworkID)).Hash(expected)
	if !bytes.Equal(unsigned.RawBytes, hash.Bytes()) {
		t.Fatalf("signing hash %x, want %x", unsigned.RawBytes, hash.Bytes())
	}
	if unsigned.ChainID != "arbitrum" {
		t.Fatalf("chain %q", unsigned.ChainID)
	}
}

func TestBuildCall_SignedCallDecodesToTheRequestedTransaction(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "arbitrum", NetworkID: evmCallTestNetworkID})
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
	call := evmCallTestCall()
	unsigned, err := adapter.BuildCall(call)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := crypto.Sign(unsigned.RawBytes, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	signed, err := adapter.FinalizeMPCSignature(unsigned, signature[:64], crypto.CompressPubkey(&privateKey.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.VerifySignedTransaction(unsigned, signed, from); err != nil {
		t.Fatal(err)
	}
	var decoded gethtypes.Transaction
	if err := decoded.UnmarshalBinary(signed.RawBytes); err != nil {
		t.Fatal(err)
	}
	if decoded.ChainId().Int64() != evmCallTestNetworkID || decoded.Nonce() != call.Nonce ||
		decoded.To().Hex() != common.HexToAddress(call.To).Hex() || decoded.Value().Cmp(call.Value) != 0 ||
		!bytes.Equal(decoded.Data(), call.Data) || decoded.Gas() != call.GasLimit || decoded.GasPrice().Cmp(call.GasPrice) != 0 {
		t.Fatalf("decoded transaction %s differs from the call", decoded.Hash().Hex())
	}
	if decoded.Hash().Hex() != signed.TxHash {
		t.Fatalf("tx hash %s, decoded %s", signed.TxHash, decoded.Hash().Hex())
	}
}

func TestBuildCall_AllowsAPlainTransferWithoutData(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "arbitrum", NetworkID: evmCallTestNetworkID})
	call := evmCallTestCall()
	call.Data = nil
	if _, err := adapter.BuildCall(call); err != nil {
		t.Fatal(err)
	}
}

func TestBuildCall_RejectsIncompleteCalls(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "arbitrum", NetworkID: evmCallTestNetworkID})
	cases := map[string]func(*EVMCall){
		"bad destination":    func(c *EVMCall) { c.To = "0x1234" },
		"nil value":          func(c *EVMCall) { c.Value = nil },
		"negative value":     func(c *EVMCall) { c.Value = big.NewInt(-1) },
		"zero gas limit":     func(c *EVMCall) { c.GasLimit = 0 },
		"nil gas price":      func(c *EVMCall) { c.GasPrice = nil },
		"zero gas price":     func(c *EVMCall) { c.GasPrice = big.NewInt(0) },
		"negative gas price": func(c *EVMCall) { c.GasPrice = big.NewInt(-2) },
	}
	for name, mutate := range cases {
		call := evmCallTestCall()
		mutate(&call)
		if _, err := adapter.BuildCall(call); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := NewEVMLive(EVMConfig{ChainIDStr: "arbitrum"}).BuildCall(evmCallTestCall()); err == nil {
		t.Error("adapter without network id: expected an error")
	}
}
