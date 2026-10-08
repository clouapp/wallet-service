package evmcall

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestTestnet_Network_AllowsOnlyTheTestnets(t *testing.T) {
	for _, chainID := range []int64{11155111, 421614, 84532, 80002, 97} {
		if _, err := TestnetNetwork(chainID); err != nil {
			t.Errorf("%d: %v", chainID, err)
		}
	}
	for _, chainID := range []int64{1, 10, 56, 137, 8453, 42161, 42170, 43114} {
		if _, err := TestnetNetwork(chainID); !errors.Is(err, ErrMainnetRefused) {
			t.Errorf("mainnet %d: %v", chainID, err)
		}
	}
	for _, chainID := range []int64{0, -1, 5, 31337} {
		if _, err := TestnetNetwork(chainID); !errors.Is(err, ErrNetworkNotAllowed) {
			t.Errorf("unlisted %d: %v", chainID, err)
		}
	}
}

func TestRequest_Validate_RejectsMalformedRequests(t *testing.T) {
	valid := Request{WalletID: uuid.New(), ChainID: testChainID, To: testInbox, Value: big.NewInt(1)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(r *Request){
		"no wallet":       func(r *Request) { r.WalletID = uuid.Nil },
		"mainnet":         func(r *Request) { r.ChainID = 1 },
		"bad destination": func(r *Request) { r.To = "0x1234" },
		"nil value":       func(r *Request) { r.Value = nil },
		"negative value":  func(r *Request) { r.Value = big.NewInt(-1) },
		"oversized data":  func(r *Request) { r.Data = make([]byte, MaxDataBytes+1) },
		"huge gas limit":  func(r *Request) { r.GasLimit = MaxGasLimit + 1 },
	}
	for name, mutate := range cases {
		request := valid
		mutate(&request)
		if err := request.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParse_Native_Amount(t *testing.T) {
	cases := map[string]string{
		"0.03": "30000000000000000", "1": "1000000000000000000", "0": "0",
		"0.000000000000000001": "1", " 2.5 ": "2500000000000000000",
	}
	for input, want := range cases {
		got, err := ParseNativeAmount(input)
		if err != nil || got.String() != want {
			t.Errorf("%q: got %v, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1", "abc", "1e18", ".5", "1.", "01", "0.0000000000000000001"} {
		if _, err := ParseNativeAmount(input); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("%q: expected ErrInvalidAmount, got %v", input, err)
		}
	}
}

func TestParse_Wei_AndFormatNative(t *testing.T) {
	if wei, err := ParseWei("30000000000000000"); err != nil || FormatNative(wei) != "0.03" {
		t.Fatalf("wei %v err %v", wei, err)
	}
	for _, input := range []string{"-1", "0.5", "x", ""} {
		if _, err := ParseWei(input); err == nil {
			t.Errorf("%q: expected an error", input)
		}
	}
	if FormatNative(big.NewInt(0)) != "0" || FormatNative(nil) != "0" || FormatNative(big.NewInt(1)) != "0.000000000000000001" {
		t.Fatal("FormatNative edge cases")
	}
}

func TestDecode_Call_Data(t *testing.T) {
	for _, empty := range []string{"", "0x", "  "} {
		if data, err := DecodeCallData(empty); err != nil || data != nil {
			t.Errorf("%q: %v %v", empty, data, err)
		}
	}
	if data, err := DecodeCallData("0x439370b1"); err != nil || len(data) != 4 {
		t.Fatalf("selector: %x %v", data, err)
	}
	for _, bad := range []string{"439370b1", "0x439370b", "0xzz", "0x" + strings.Repeat("00", MaxDataBytes+1)} {
		if _, err := DecodeCallData(bad); err == nil {
			t.Errorf("%.20q: expected an error", bad)
		}
	}
}
