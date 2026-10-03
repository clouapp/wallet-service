package evmcall

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
)

const (
	hexPrefix         = "0x"
	minPassphraseSize = 12
)

var (
	tagPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,80}$`)
	decimalPattern   = regexp.MustCompile(`^(0|[1-9][0-9]{0,30})(\.[0-9]{1,18})?$`)
	ErrInvalidAmount = errors.New("invalid amount")
)

// Request is one evm:call: what to send, from which wallet, on which testnet.
// GasLimit 0 means eth_estimateGas plus a margin; Tag names the single broadcast.
type Request struct {
	WalletID uuid.UUID
	ChainID  int64
	To       string
	Data     []byte
	Value    *big.Int
	GasLimit uint64
	Tag      string
}

// Validate checks everything that needs no node or database.
func (r Request) Validate() error {
	if r.WalletID == uuid.Nil {
		return fmt.Errorf("wallet id is required")
	}
	if _, err := TestnetNetwork(r.ChainID); err != nil {
		return err
	}
	if !common.IsHexAddress(r.To) {
		return fmt.Errorf("to %q is not an EVM address", r.To)
	}
	if r.Value == nil || r.Value.Sign() < 0 {
		return fmt.Errorf("value must be zero or positive")
	}
	if len(r.Data) > MaxDataBytes {
		return fmt.Errorf("data has %d bytes, more than %d", len(r.Data), MaxDataBytes)
	}
	if r.GasLimit > MaxGasLimit {
		return fmt.Errorf("gas limit %d exceeds %d", r.GasLimit, MaxGasLimit)
	}
	return nil
}

// ValidateBroadcast adds what only a broadcast needs.
func (r Request) ValidateBroadcast(passphrase string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if !tagPattern.MatchString(r.Tag) {
		return fmt.Errorf("tag %q must match %s", r.Tag, tagPattern)
	}
	if len(passphrase) < minPassphraseSize {
		return fmt.Errorf("passphrase must be at least %d characters", minPassphraseSize)
	}
	return nil
}

// DecodeCallData accepts "" or "0x" for no calldata, else 0x-prefixed hex.
func DecodeCallData(data string) ([]byte, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == hexPrefix {
		return nil, nil
	}
	if !strings.HasPrefix(trimmed, hexPrefix) {
		return nil, fmt.Errorf("data must be 0x-prefixed hex")
	}
	decoded, err := hex.DecodeString(trimmed[len(hexPrefix):])
	if err != nil {
		return nil, fmt.Errorf("data is not valid hex")
	}
	if len(decoded) > MaxDataBytes {
		return nil, fmt.Errorf("data exceeds %d bytes", MaxDataBytes)
	}
	return decoded, nil
}

// ParseNativeAmount turns a decimal amount of the native asset ("0.03") into wei.
func ParseNativeAmount(amount string) (*big.Int, error) {
	trimmed := strings.TrimSpace(amount)
	if !decimalPattern.MatchString(trimmed) {
		return nil, fmt.Errorf("%w %q: use a non-negative decimal with at most %d decimals", ErrInvalidAmount, amount, nativeDecimals)
	}
	whole, fraction, _ := strings.Cut(trimmed, ".")
	digits := whole + fraction + strings.Repeat("0", nativeDecimals-len(fraction))
	wei, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrInvalidAmount, amount)
	}
	return wei, nil
}

// ParseWei parses a non-negative integer amount in wei.
func ParseWei(amount string) (*big.Int, error) {
	wei, ok := new(big.Int).SetString(strings.TrimSpace(amount), 10)
	if !ok || wei.Sign() < 0 {
		return nil, fmt.Errorf("%w %q: wei must be a non-negative integer", ErrInvalidAmount, amount)
	}
	return wei, nil
}

// FormatNative renders wei as a decimal amount of the native asset.
func FormatNative(wei *big.Int) string {
	if wei == nil {
		return "0"
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(nativeDecimals), nil)
	whole, fraction := new(big.Int).QuoRem(wei, scale, new(big.Int))
	if fraction.Sign() == 0 {
		return whole.String()
	}
	padded := fmt.Sprintf("%0*s", nativeDecimals, fraction.String())
	return whole.String() + "." + strings.TrimRight(padded, "0")
}
