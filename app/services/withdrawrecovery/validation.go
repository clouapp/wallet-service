package withdrawrecovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	evmtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/evmcall"
)

const MaxObservationAge = 2 * time.Minute
const MaxReviewAge = 30 * time.Minute

var unsignedDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var shaDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var txDigest = regexp.MustCompile(`^0x[a-fA-F0-9]{64}$`)

func Canonical(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Hash(value any) (string, error) {
	raw, err := Canonical(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:]), nil
}

func refused(reason string) error { return fmt.Errorf("%w: %s", ErrRefused, reason) }

func integer(value string, positive bool) (*big.Int, error) {
	if !unsignedDecimal.MatchString(value) {
		return nil, refused("invalid integer amount")
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || (positive && n.Sign() == 0) || n.BitLen() > 256 {
		return nil, refused("invalid integer amount")
	}
	return n, nil
}

func address(value string) bool {
	return common.IsHexAddress(value) && common.HexToAddress(value) != (common.Address{})
}

func identity(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\n\r\x00")
}

func (b Binding) Validate() error {
	if b.OperationID == [16]byte{} || b.OriginalTransactionID == [16]byte{} || b.AccountID == [16]byte{} || b.WalletID == [16]byte{} || b.SourceAddressID == [16]byte{} {
		return refused("missing immutable identity")
	}
	if !txDigest.MatchString(b.OriginalTxHash) || !address(b.SourceAddress) || !address(b.Recipient) || !address(b.TokenContract) {
		return refused("invalid EVM binding")
	}
	if b.Nonce == nil {
		return refused("exact original nonce is required, including zero")
	}
	if _, err := evmcall.TestnetNetwork(b.NetworkID); err != nil {
		return refused("only allowlisted EVM testnets may recover")
	}
	if !identity(b.Chain) || !identity(b.Asset) || b.SourceDerivationIndex < 0 || b.TokenDecimals < 0 || b.TokenDecimals > 36 || b.RequiredConfirmations < 1 || b.SourceExternalUserID == "" {
		return refused("invalid chain, asset or child binding")
	}
	if !shaDigest.MatchString(b.RequestHash) || !shaDigest.MatchString(b.ProviderFingerprint) {
		return refused("request and provider fingerprints are required")
	}
	if b.Finality != models.FinalityConfirmations && b.Finality != models.FinalityFinalizedTag {
		return refused("unsupported EVM finality")
	}
	_, err := integer(b.AmountRaw, true)
	return err
}

func (f FeeEnvelope) Validate() error {
	if f.MaxGasLimit == 0 || f.MaxGasLimit > evmcall.MaxGasLimit {
		return refused("invalid gas limit envelope")
	}
	price, err := integer(f.MaxGasPriceWei, true)
	if err != nil || price.Cmp(big.NewInt(evmcall.MaxGasPriceWei)) > 0 {
		return refused("invalid gas price envelope")
	}
	_, err = integer(f.MaxNativeFeeWei, true)
	return err
}

func (g *GasSeedPlan) validate(b Binding) error {
	if g == nil {
		return nil
	}
	if g.BaseAddressID == [16]byte{} || g.BaseAddressID == b.SourceAddressID || !address(g.BaseAddress) || strings.EqualFold(g.BaseAddress, b.SourceAddress) || g.Nonce == nil {
		return refused("invalid gas prerequisite binding")
	}
	if _, err := integer(g.AmountWei, true); err != nil {
		return err
	}
	_, err := integer(g.MaxFeeWei, true)
	return err
}

func (o *Observation) validate(b Binding, fees FeeEnvelope, now time.Time) (*big.Int, error) {
	if o == nil {
		return nil, refused("missing observation")
	}
	if err := fresh(o.ObservedAt, MaxObservationAge, now); err != nil {
		return nil, err
	}
	expected, _ := Hash(b)
	actual, _ := Hash(o.Binding)
	if expected != actual {
		return nil, refused("current economic binding changed")
	}
	if o.AccountEnvironment != models.EnvironmentTest || o.WalletStatus != "active" || o.WalletFrozen || !o.ChainTestnet || o.AdapterType != models.AdapterTypeEVM || !o.RPCsAgree || o.RPCNetworkID != b.NetworkID {
		return nil, refused("current wallet, account or network is ineligible")
	}
	if o.OriginalStatus != "failed" || o.BroadcastOutcome != chain.BroadcastOutcomeRefused {
		return nil, refused("original transaction is not a refused broadcast")
	}
	if o.LatestNonce == nil || o.PendingNonce == nil {
		return nil, refused("both nonce observations are required")
	}
	if o.OriginalKnown || o.CandidateKnown || o.ReceiptObserved || *o.LatestNonce != *b.Nonce || *o.PendingNonce != *b.Nonce {
		return nil, ErrNeedsReconciliation
	}
	tokens, err := integer(o.SourceTokenBalanceRaw, false)
	if err != nil {
		return nil, err
	}
	required, _ := integer(b.AmountRaw, true)
	if tokens.Cmp(required) < 0 {
		return nil, refused("source token balance changed")
	}
	if _, err := integer(o.SourceNativeBalanceWei, false); err != nil {
		return nil, err
	}
	return o.Quote.fee(fees)
}

func fresh(at time.Time, age time.Duration, now time.Time) error {
	if at.IsZero() || at.After(now.Add(5*time.Second)) || now.Sub(at) > age {
		return refused("observation or review is stale")
	}
	return nil
}

func (q FeeQuote) fee(fees FeeEnvelope) (*big.Int, error) {
	if q.GasLimit == 0 || q.GasLimit > fees.MaxGasLimit {
		return nil, refused("gas limit exceeds reviewed envelope")
	}
	price, err := integer(q.GasPriceWei, true)
	if err != nil {
		return nil, err
	}
	maxPrice, _ := integer(fees.MaxGasPriceWei, true)
	if price.Cmp(maxPrice) > 0 {
		return nil, refused("gas price exceeds reviewed envelope")
	}
	l1, err := integer(q.L1FeeWei, false)
	if err != nil {
		return nil, err
	}
	fee := new(big.Int).Add(new(big.Int).Mul(price, new(big.Int).SetUint64(q.GasLimit)), l1)
	maxFee, _ := integer(fees.MaxNativeFeeWei, true)
	if fee.Cmp(maxFee) > 0 {
		return nil, refused("native fee exceeds reviewed envelope")
	}
	return fee, nil
}

func validateReview(p Proposal, review Review, operator, revision string, now time.Time) error {
	if err := p.Binding.Validate(); err != nil {
		return err
	}
	if err := p.Fees.Validate(); err != nil {
		return err
	}
	if err := p.GasSeed.validate(p.Binding); err != nil {
		return err
	}
	if !identity(p.PreparedBy) || !identity(review.ReviewedBy) || !identity(operator) || review.ReviewedBy == p.PreparedBy || review.ReviewedBy == operator {
		return refused("an independent reviewer is required")
	}
	if p.CodeRevision == "" || p.CodeRevision != revision {
		return refused("code revision changed")
	}
	hash, err := Hash(p)
	if err != nil {
		return err
	}
	if !review.Approved || review.ProposalHash != hash || !shaDigest.MatchString(review.EvidenceHash) {
		return refused("review does not approve this proposal")
	}
	if preparedErr := fresh(p.PreparedAt, MaxReviewAge, now); preparedErr != nil {
		return preparedErr
	}
	if reviewedErr := fresh(review.ReviewedAt, MaxReviewAge, now); reviewedErr != nil {
		return reviewedErr
	}
	if review.ReviewedAt.Before(p.PreparedAt) || !p.ExpiresAt.After(now) || !review.ExpiresAt.After(now) || p.ExpiresAt.After(p.PreparedAt.Add(MaxReviewAge)) || review.ExpiresAt.After(p.ExpiresAt) {
		return refused("review or proposal expiry is invalid")
	}
	_, err = p.Observation.validate(p.Binding, p.Fees, p.PreparedAt)
	return err
}

func transferData(b Binding) []byte {
	amount, _ := integer(b.AmountRaw, true)
	data := make([]byte, 68)
	copy(data, []byte{0xa9, 0x05, 0x9c, 0xbb})
	copy(data[16:36], common.HexToAddress(b.Recipient).Bytes())
	amount.FillBytes(data[36:68])
	return data
}

type frozenCall struct {
	Nonce    uint64 `json:"nonce"`
	To       string `json:"to"`
	Value    string `json:"value"`
	Data     string `json:"data"`
	GasLimit uint64 `json:"gas_limit"`
	GasPrice string `json:"gas_price"`
	L1Fee    string `json:"l1_fee"`
}

func makeCall(b Binding, quote FeeQuote) frozenCall {
	return frozenCall{Nonce: *b.Nonce, To: b.TokenContract, Value: "0", Data: hex.EncodeToString(transferData(b)), GasLimit: quote.GasLimit, GasPrice: quote.GasPriceWei, L1Fee: quote.L1FeeWei}
}

func (c frozenCall) validate(b Binding, fees FeeEnvelope) (chain.EVMCall, *big.Int, error) {
	var empty chain.EVMCall
	if c.Nonce != *b.Nonce || !strings.EqualFold(c.To, b.TokenContract) || c.Value != "0" || c.Data != hex.EncodeToString(transferData(b)) {
		return empty, nil, refused("stored call differs from immutable transfer")
	}
	fee, err := (FeeQuote{GasLimit: c.GasLimit, GasPriceWei: c.GasPrice, L1FeeWei: c.L1Fee}).fee(fees)
	if err != nil {
		return empty, nil, err
	}
	price, _ := integer(c.GasPrice, true)
	return chain.EVMCall{Nonce: c.Nonce, To: c.To, Value: big.NewInt(0), Data: transferData(b), GasLimit: c.GasLimit, GasPrice: price}, fee, nil
}

func verifySigned(raw []byte, b Binding, c frozenCall, fees FeeEnvelope) (string, error) {
	if _, _, err := c.validate(b, fees); err != nil {
		return "", err
	}
	var tx evmtypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", refused("invalid signed transaction")
	}
	if tx.Type() != evmtypes.LegacyTxType || !tx.Protected() || tx.ChainId().Cmp(big.NewInt(b.NetworkID)) != 0 || tx.Nonce() != *b.Nonce || tx.To() == nil || *tx.To() != common.HexToAddress(b.TokenContract) || tx.Value().Sign() != 0 || !bytes.Equal(tx.Data(), transferData(b)) || tx.Gas() != c.GasLimit || tx.GasPrice().String() != c.GasPrice {
		return "", refused("signed transaction violates immutable transfer or fee envelope")
	}
	from, err := evmtypes.Sender(evmtypes.LatestSignerForChainID(big.NewInt(b.NetworkID)), &tx)
	if err != nil || from != common.HexToAddress(b.SourceAddress) {
		return "", refused("signed transaction uses another sender")
	}
	return tx.Hash().Hex(), nil
}
