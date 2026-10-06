package evm

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	pkgtypes "github.com/macrowallets/waas/pkg/types"
)

func TestEVM_Validate_Address(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: "http://fake"})

	tests := []struct {
		addr string
		want bool
	}{
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", true},
		{"0x0000000000000000000000000000000000000000", true},
		{"0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", true},
		{"0x742d35cc6634c0532925a3b844bc9e7595f2bd12", true},   // lowercase
		{"742d35Cc6634C0532925a3b844Bc9e7595f2bD12", false},    // missing 0x
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD1", false},   // too short
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD123", false}, // too long
		{"0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG", false},  // invalid hex
		{"", false},
		{"0x", false},
		{"hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := adapter.ValidateAddress(tt.addr); got != tt.want {
				t.Errorf("ValidateAddress(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestLive_EVM_Identity(t *testing.T) {
	eth := NewEVMLive(EVMConfig{ChainIDStr: "eth", ChainName: "Ethereum", NativeSymbol: "eth", Confirmations: 12})
	poly := NewEVMLive(EVMConfig{ChainIDStr: "polygon", ChainName: "Polygon", NativeSymbol: "matic", Confirmations: 128})

	if eth.ID() != "eth" {
		t.Errorf("expected eth, got %s", eth.ID())
	}
	if eth.Name() != "Ethereum" {
		t.Errorf("expected Ethereum, got %s", eth.Name())
	}
	if eth.NativeAsset() != "eth" {
		t.Errorf("expected eth, got %s", eth.NativeAsset())
	}
	if eth.RequiredConfirmations() != 12 {
		t.Errorf("expected 12, got %d", eth.RequiredConfirmations())
	}
	if poly.ID() != "polygon" {
		t.Errorf("expected polygon, got %s", poly.ID())
	}
	if poly.NativeAsset() != "matic" {
		t.Errorf("expected matic, got %s", poly.NativeAsset())
	}
	if poly.RequiredConfirmations() != 128 {
		t.Errorf("expected 128, got %d", poly.RequiredConfirmations())
	}
}

func TestEncode_ERC20_Transfer(t *testing.T) {
	to := "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12"
	amount := big.NewInt(1000000) // 1 USDT (6 decimals)

	data := encodeERC20Transfer(to, amount)

	if len(data) != 68 {
		t.Fatalf("expected 68 bytes, got %d", len(data))
	}
	// Check function selector: transfer(address,uint256) = 0xa9059cbb
	if data[0] != 0xa9 || data[1] != 0x05 || data[2] != 0x9c || data[3] != 0xbb {
		t.Error("wrong function selector")
	}
}

func TestHex_To_BigInt(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"0x0", 0},
		{"0x1", 1},
		{"0xa", 10},
		{"0xff", 255},
		{"0x100", 256},
		{"0xde0b6b3a7640000", 1000000000000000000}, // 1 ETH in wei
		{"", 0},
		{"0x", 0},
		{"0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hexToBigInt(tt.input)
			if got.Int64() != tt.want {
				t.Errorf("hexToBigInt(%s) = %d, want %d", tt.input, got.Int64(), tt.want)
			}
		})
	}
}

func TestHex_To_Uint64(t *testing.T) {
	tests := []struct {
		input string
		want  uint64
	}{
		{"0x0", 0},
		{"0x1", 1},
		{"0xff", 255},
		{"0x12f2b3", 1241779},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := hexToUint64(tt.input); got != tt.want {
				t.Errorf("hexToUint64(%s) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestLive_Pad_Addr(t *testing.T) {
	got := padAddr("0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12")
	if len(got) != 64 {
		t.Errorf("expected 64 chars, got %d", len(got))
	}
	// Should be left-padded with zeros
	if got[:24] != "000000000000000000000000" {
		t.Error("expected left zero padding")
	}
}

func TestTopic_To_Addr(t *testing.T) {
	topic := "0x000000000000000000000000742d35cc6634c0532925a3b844bc9e7595f2bd12"
	got := topicToAddr(topic)
	if got != "0x742d35cc6634c0532925a3b844bc9e7595f2bd12" {
		t.Errorf("expected address, got %s", got)
	}
}

func TestLive_Fmt_Units(t *testing.T) {
	tests := []struct {
		amount   *big.Int
		decimals uint8
		want     string
	}{
		{big.NewInt(1000000), 6, "1"},               // 1 USDT
		{big.NewInt(1500000), 6, "1.5"},             // 1.5 USDT
		{big.NewInt(1000000000000000000), 18, "1"},  // 1 ETH
		{big.NewInt(500000000000000000), 18, "0.5"}, // 0.5 ETH
		{big.NewInt(0), 18, "0"},
		{big.NewInt(1), 18, "0.000000000000000001"},
		{nil, 18, "0"},
	}
	for _, tt := range tests {
		name := "nil"
		if tt.amount != nil {
			name = tt.amount.String()
		}
		t.Run(name, func(t *testing.T) {
			got := fmtUnits(tt.amount, tt.decimals)
			if got != tt.want {
				t.Errorf("fmtUnits(%v, %d) = %s, want %s", tt.amount, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestEVM_Finalize_MPCSignatureBuildsBroadcastableTransaction(t *testing.T) {
	t.Parallel()

	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	const chainID = int64(11155111)
	const nonce = uint64(7)
	const gasLimit = uint64(21_000)
	to := "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12"
	value := big.NewInt(1_000_000_000_000_000)
	gasPrice := big.NewInt(2_000_000_000)
	signer := gethtypes.LatestSignerForChainID(big.NewInt(chainID))
	unsignedTransaction := gethtypes.NewTransaction(
		nonce,
		common.HexToAddress(to),
		value,
		gasLimit,
		gasPrice,
		nil,
	)
	hash := signer.Hash(unsignedTransaction).Bytes()
	signature, err := crypto.Sign(hash, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	adapter := NewEVMLive(EVMConfig{ChainIDStr: "teth", NetworkID: chainID})
	unsigned := &pkgtypes.UnsignedTx{
		ChainID:  "teth",
		RawBytes: hash,
		Metadata: map[string]any{
			"nonce":     nonce,
			"to":        to,
			"value":     value.String(),
			"gas_limit": gasLimit,
			"gas_price": gasPrice.String(),
			"chain_id":  chainID,
			"data":      []byte(nil),
		},
	}

	signed, err := adapter.FinalizeMPCSignature(
		unsigned,
		signature[:64],
		crypto.CompressPubkey(&privateKey.PublicKey),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(signed.RawBytes) == 0 {
		t.Fatal("expected serialized signed transaction")
	}

	var decoded gethtypes.Transaction
	if err := decoded.UnmarshalBinary(signed.RawBytes); err != nil {
		t.Fatalf("decode signed transaction: %v", err)
	}
	sender, err := gethtypes.Sender(signer, &decoded)
	if err != nil {
		t.Fatalf("recover sender: %v", err)
	}
	expectedSender := crypto.PubkeyToAddress(privateKey.PublicKey)
	if sender != expectedSender {
		t.Fatalf("sender = %s, want %s", sender, expectedSender)
	}
	if decoded.Nonce() != nonce || decoded.To() == nil || *decoded.To() != common.HexToAddress(to) {
		t.Fatalf("unexpected signed transaction fields")
	}
	if !bytes.Equal(decoded.Data(), nil) || decoded.Value().Cmp(value) != 0 {
		t.Fatalf("unexpected transaction payload")
	}
}

func TestEVM_Finalize_MPCSignatureRejectsWrongPublicKey(t *testing.T) {
	t.Parallel()

	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	hash := crypto.Keccak256([]byte("e2e"))
	signature, err := crypto.Sign(hash, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "teth", NetworkID: 11155111})
	unsigned := &pkgtypes.UnsignedTx{
		ChainID:  "teth",
		RawBytes: hash,
		Metadata: map[string]any{
			"nonce":     uint64(0),
			"to":        "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
			"value":     "1",
			"gas_limit": uint64(21_000),
			"gas_price": "1",
			"chain_id":  int64(11155111),
			"data":      []byte(nil),
		},
	}

	if _, err := adapter.FinalizeMPCSignature(
		unsigned,
		signature[:64],
		crypto.CompressPubkey(&otherKey.PublicKey),
	); err == nil {
		t.Fatal("expected public key mismatch error")
	}
}

func TestBuffered_EVM_GasPrice(t *testing.T) {
	t.Parallel()

	input := big.NewInt(1_300_000_000)
	got := bufferedEVMGasPrice(input)
	if got.String() != "2600000000" {
		t.Fatalf("buffered gas price = %s", got)
	}
	if input.String() != "1300000000" {
		t.Fatalf("input gas price was mutated to %s", input)
	}
}
