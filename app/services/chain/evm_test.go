package chain

import (
	"math/big"
	"testing"
)

func TestEVM_ValidateAddress(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: "http://fake"})

	tests := []struct {
		addr string
		want bool
	}{
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", true},
		{"0x0000000000000000000000000000000000000000", true},
		{"0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", true},
		{"0x742d35cc6634c0532925a3b844bc9e7595f2bd12", true}, // lowercase
		{"742d35Cc6634C0532925a3b844Bc9e7595f2bD12", false},  // missing 0x
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD1", false},  // too short
		{"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD123", false}, // too long
		{"0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG", false}, // invalid hex
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

func TestEVM_Identity(t *testing.T) {
	eth := NewEVMLive(EVMConfig{ChainIDStr: "eth", ChainName: "Ethereum", NativeSymbol: "eth", Confirmations: 12})
	poly := NewEVMLive(EVMConfig{ChainIDStr: "polygon", ChainName: "Polygon", NativeSymbol: "matic", Confirmations: 128})

	if eth.ID() != "eth" { t.Errorf("expected eth, got %s", eth.ID()) }
	if eth.Name() != "Ethereum" { t.Errorf("expected Ethereum, got %s", eth.Name()) }
	if eth.NativeAsset() != "eth" { t.Errorf("expected eth, got %s", eth.NativeAsset()) }
	if eth.RequiredConfirmations() != 12 { t.Errorf("expected 12, got %d", eth.RequiredConfirmations()) }
	if poly.ID() != "polygon" { t.Errorf("expected polygon, got %s", poly.ID()) }
	if poly.NativeAsset() != "matic" { t.Errorf("expected matic, got %s", poly.NativeAsset()) }
	if poly.RequiredConfirmations() != 128 { t.Errorf("expected 128, got %d", poly.RequiredConfirmations()) }
}

func TestEncodeERC20Transfer(t *testing.T) {
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

func TestHexToBigInt(t *testing.T) {
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

func TestHexToUint64(t *testing.T) {
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

func TestPadAddr(t *testing.T) {
	got := padAddr("0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12")
	if len(got) != 64 {
		t.Errorf("expected 64 chars, got %d", len(got))
	}
	// Should be left-padded with zeros
	if got[:24] != "000000000000000000000000" {
		t.Error("expected left zero padding")
	}
}

func TestTopicToAddr(t *testing.T) {
	topic := "0x000000000000000000000000742d35cc6634c0532925a3b844bc9e7595f2bd12"
	got := topicToAddr(topic)
	if got != "0x742d35cc6634c0532925a3b844bc9e7595f2bd12" {
		t.Errorf("expected address, got %s", got)
	}
}

func TestFmtUnits(t *testing.T) {
	tests := []struct {
		amount   *big.Int
		decimals uint8
		want     string
	}{
		{big.NewInt(1000000), 6, "1"},                          // 1 USDT
		{big.NewInt(1500000), 6, "1.5"},                        // 1.5 USDT
		{big.NewInt(1000000000000000000), 18, "1"},             // 1 ETH
		{big.NewInt(500000000000000000), 18, "0.5"},            // 0.5 ETH
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
