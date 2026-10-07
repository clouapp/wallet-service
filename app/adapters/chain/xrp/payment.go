package xrp

import (
	"context"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// altnetNetworkID is the network id server_info reports for
	// s.altnet.rippletest.net. Mainnet is not 1. A network id of 1024 or less
	// must omit the NetworkID field, so this payment does not write one.
	altnetNetworkID = 1

	xrpLastLedgerWindow = 20
	// xrpMaxFeeDrops refuses a congested open-ledger fee instead of paying it.
	xrpMaxFeeDrops = 1_000

	xrpTypeUInt16  = 1
	xrpTypeUInt32  = 2
	xrpTypeAmount  = 6
	xrpTypeVL      = 7
	xrpTypeAccount = 8

	xrpFieldTransactionType    = 2
	xrpFieldSequence           = 4
	xrpFieldLastLedgerSequence = 27
	xrpFieldAmount             = 1
	xrpFieldFee                = 8
	xrpFieldSigningPubKey      = 3
	xrpFieldTxnSignature       = 4
	xrpFieldAccount            = 1
	xrpFieldDestination        = 3

	xrpPaymentType = 0

	// xrpAmountPositive is the bit rippled sets on a native (non-IOU) amount.
	xrpAmountPositive = uint64(0x4000000000000000)
	xrpAmountMax      = uint64(0x3FFFFFFFFFFFFFFF)
)

var (
	xrpSignPrefix = []byte{0x53, 0x54, 0x58, 0x00} // "STX\0"
	xrpHashPrefix = []byte{0x54, 0x58, 0x4E, 0x00} // "TXN\0"

	xrpMainnetHosts = map[string]struct{}{
		"s1.ripple.com":   {},
		"s2.ripple.com":   {},
		"xrplcluster.com": {},
		"xrpl.ws":         {},
		"s1.ripple.com.":  {},
	}
)

// xrpPayment is a native Payment. Flags and NetworkID are omitted: flags
// default to 0, and network id 1 must not carry a NetworkID field.
type xrpPayment struct {
	Account            []byte
	Destination        []byte
	Amount             uint64
	Fee                uint64
	Sequence           uint32
	LastLedgerSequence uint32
	SigningPubKey      []byte
	TxnSignature       []byte
}

func (a *Live) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	if err := a.requireAltnet(ctx); err != nil {
		return nil, err
	}
	if req.Token != nil {
		return nil, fmt.Errorf("xrp: no issued currencies in this experiment")
	}
	if req.Asset != "" && !types.SameAssetSymbol(req.Asset, a.cfg.NativeSymbol) {
		return nil, fmt.Errorf("xrp: asset %q is not %s", req.Asset, a.cfg.NativeSymbol)
	}
	if !addressing.IsXRPClassicAddress(req.From) || !addressing.IsXRPClassicAddress(req.To) {
		return nil, fmt.Errorf("xrp payment: from and to must be classic addresses")
	}
	if req.From == req.To {
		return nil, fmt.Errorf("xrp payment: from and to are the same address")
	}
	if req.Amount == nil || req.Amount.Sign() <= 0 || !req.Amount.IsUint64() {
		return nil, fmt.Errorf("xrp payment: amount must be a positive number of drops")
	}
	amount := req.Amount.Uint64()

	facts, err := a.ledgerFacts(ctx)
	if err != nil {
		return nil, err
	}
	if facts.NetworkID != altnetNetworkID {
		return nil, fmt.Errorf("xrp rpc network_id is %d, want altnet %d", facts.NetworkID, altnetNetworkID)
	}
	fee, err := a.openLedgerFeeDrops(ctx)
	if err != nil {
		return nil, err
	}
	if !fee.IsUint64() || fee.Uint64() == 0 || fee.Uint64() > xrpMaxFeeDrops {
		return nil, fmt.Errorf("xrp open ledger fee %s drops is outside 1..%d", fee, xrpMaxFeeDrops)
	}
	balance, err := a.accountDrops(ctx, req.From)
	if err != nil {
		return nil, err
	}
	if err := xrpReserveAllows(balance, req.Amount, fee, facts.ReserveDrops); err != nil {
		return nil, err
	}
	destFunded, err := a.accountExists(ctx, req.To)
	if err != nil {
		return nil, err
	}
	if !destFunded && req.Amount.Cmp(facts.ReserveDrops) < 0 {
		return nil, fmt.Errorf("xrp payment of %s drops is below the %s-drop reserve a new account needs", req.Amount, facts.ReserveDrops)
	}
	sequence, err := a.accountSequence(ctx, req.From)
	if err != nil {
		return nil, err
	}
	if sequence > uint64(^uint32(0)) {
		return nil, fmt.Errorf("xrp sequence does not fit a uint32")
	}
	accountID, err := addressing.DecodeXRPClassicAddress(req.From)
	if err != nil {
		return nil, err
	}
	destinationID, err := addressing.DecodeXRPClassicAddress(req.To)
	if err != nil {
		return nil, err
	}
	last := facts.LedgerIndex + xrpLastLedgerWindow
	if last > uint64(^uint32(0)) {
		return nil, fmt.Errorf("xrp last ledger sequence does not fit a uint32")
	}
	payment := xrpPayment{
		Account:            accountID,
		Destination:        destinationID,
		Amount:             amount,
		Fee:                fee.Uint64(),
		Sequence:           uint32(sequence),
		LastLedgerSequence: uint32(last),
	}
	unsignedBlob, err := payment.serialize(false, false)
	if err != nil {
		return nil, err
	}
	return &types.UnsignedTx{
		ChainID:        a.cfg.ChainIDStr,
		RawBytes:       unsignedBlob,
		TransferAmount: new(big.Int).SetUint64(amount),
		Metadata: map[string]interface{}{
			"unsigned_hex": hex.EncodeToString(unsignedBlob),
			"account":      req.From,
			"destination":  req.To,
			"amount_drops": req.Amount.String(),
			"sequence":     fmt.Sprintf("%d", sequence),
			"fee_drops":    fee.String(),
			"last_ledger":  fmt.Sprintf("%d", last),
		},
	}, nil
}

func (a *Live) SignTransaction(_ context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	if unsigned == nil {
		return nil, fmt.Errorf("xrp sign: unsigned transaction is required")
	}
	if len(privateKey) != 32 {
		return nil, fmt.Errorf("xrp sign: private key must be 32 bytes")
	}
	blob, err := hex.DecodeString(metadataString(unsigned.Metadata, "unsigned_hex"))
	if err != nil || len(blob) == 0 || !bytesEqual(blob, unsigned.RawBytes) {
		return nil, fmt.Errorf("xrp sign: unsigned payment is missing")
	}
	payment, err := parseXRPPayment(blob)
	if err != nil {
		return nil, fmt.Errorf("xrp sign: %w", err)
	}
	if len(payment.TxnSignature) != 0 || len(payment.SigningPubKey) != 0 {
		return nil, fmt.Errorf("xrp sign: payment already has a key or a signature")
	}
	pub := compressedPub(privateKey)
	account, err := addressing.DeriveXRPAddress(pub)
	if err != nil {
		return nil, fmt.Errorf("xrp sign: %w", err)
	}
	from := metadataString(unsigned.Metadata, "account")
	if account != from {
		return nil, fmt.Errorf("xrp sign: key does not own %s", from)
	}
	wantID, err := addressing.DecodeXRPClassicAddress(from)
	if err != nil || !bytesEqual(wantID, payment.Account) {
		return nil, fmt.Errorf("xrp sign: payment account is not %s", from)
	}
	payment.SigningPubKey = pub
	preimage, err := payment.serialize(true, false)
	if err != nil {
		return nil, err
	}
	priv, _ := btcec.PrivKeyFromBytes(privateKey)
	sig := ecdsa.Sign(priv, xrpSHA512Half(xrpSignPrefix, preimage))
	payment.TxnSignature = sig.Serialize()
	signedBlob, err := payment.serialize(true, true)
	if err != nil {
		return nil, err
	}
	txHash := strings.ToUpper(hex.EncodeToString(xrpSHA512Half(xrpHashPrefix, signedBlob)))
	return &types.SignedTx{ChainID: unsigned.ChainID, TxHash: txHash, RawBytes: signedBlob}, nil
}

func (a *Live) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	if signed == nil || len(signed.RawBytes) == 0 || signed.TxHash == "" {
		return "", fmt.Errorf("xrp broadcast: signed transaction is required")
	}
	if err := a.requireAltnet(ctx); err != nil {
		return "", err
	}
	want := strings.ToUpper(hex.EncodeToString(xrpSHA512Half(xrpHashPrefix, signed.RawBytes)))
	if !strings.EqualFold(want, signed.TxHash) {
		return "", fmt.Errorf("xrp broadcast: tx hash does not match the signed bytes")
	}
	payment, err := parseXRPPayment(signed.RawBytes)
	if err != nil || len(payment.TxnSignature) == 0 || len(payment.SigningPubKey) == 0 {
		return "", fmt.Errorf("xrp broadcast: signed payment is incomplete")
	}
	var result struct {
		xrpRPCError
		EngineResult string `json:"engine_result"`
		TxJSON       struct {
			Hash string `json:"hash"`
		} `json:"tx_json"`
	}
	err = a.call(ctx, "submit", []any{map[string]string{
		"tx_blob": hex.EncodeToString(signed.RawBytes),
	}}, &result)
	if err != nil {
		return "", err
	}
	if result.Status != "success" {
		return "", fmt.Errorf("xrp submit: %s", xrpErrorCode(result.xrpRPCError))
	}
	if result.TxJSON.Hash != "" && !strings.EqualFold(result.TxJSON.Hash, signed.TxHash) {
		return "", fmt.Errorf("xrp submit returned a different hash")
	}
	if !xrpSubmitAccepted(result.EngineResult) {
		return "", fmt.Errorf("xrp submit: %s", result.EngineResult)
	}
	return strings.ToUpper(signed.TxHash), nil
}

func (a *Live) EstimateFee(ctx context.Context, req types.TransferRequest) (*types.FeeEstimate, error) {
	if err := a.requireAltnet(ctx); err != nil {
		return nil, err
	}
	if req.Token != nil || (req.Asset != "" && !types.SameAssetSymbol(req.Asset, a.cfg.NativeSymbol)) {
		return nil, fmt.Errorf("xrp: no issued currencies in this experiment")
	}
	fee, err := a.openLedgerFeeDrops(ctx)
	if err != nil {
		return nil, err
	}
	return &types.FeeEstimate{Fee: fee.String(), FeeAsset: a.cfg.NativeSymbol}, nil
}

func (a *Live) VerifySignedTransaction(unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error {
	if unsigned == nil || signed == nil {
		return fmt.Errorf("xrp verify: transaction is required")
	}
	if !addressing.IsXRPClassicAddress(from) {
		return fmt.Errorf("xrp verify: %q is not a classic address", from)
	}
	want := strings.ToUpper(hex.EncodeToString(xrpSHA512Half(xrpHashPrefix, signed.RawBytes)))
	if !strings.EqualFold(want, signed.TxHash) {
		return fmt.Errorf("xrp verify: tx hash does not match the signed bytes")
	}
	payment, err := parseXRPPayment(signed.RawBytes)
	if err != nil {
		return fmt.Errorf("xrp verify: %w", err)
	}
	if len(payment.SigningPubKey) != 33 || len(payment.TxnSignature) == 0 {
		return fmt.Errorf("xrp verify: signature is missing")
	}
	account, err := addressing.DeriveXRPAddress(payment.SigningPubKey)
	if err != nil || account != from {
		return fmt.Errorf("xrp verify: signing key does not own %s", from)
	}
	fromID, err := addressing.DecodeXRPClassicAddress(from)
	if err != nil || !bytesEqual(fromID, payment.Account) {
		return fmt.Errorf("xrp verify: payment account is not %s", from)
	}
	if metadataString(unsigned.Metadata, "account") != from {
		return fmt.Errorf("xrp verify: unsigned payment is not from %s", from)
	}
	if metadataString(unsigned.Metadata, "destination") != "" {
		destID, err := addressing.DecodeXRPClassicAddress(metadataString(unsigned.Metadata, "destination"))
		if err != nil || !bytesEqual(destID, payment.Destination) {
			return fmt.Errorf("xrp verify: destination does not match the unsigned payment")
		}
	}
	pub, err := btcec.ParsePubKey(payment.SigningPubKey)
	if err != nil {
		return fmt.Errorf("xrp verify: signing public key")
	}
	sig, err := ecdsa.ParseDERSignature(payment.TxnSignature)
	if err != nil {
		return fmt.Errorf("xrp verify: signature")
	}
	preimage, err := xrpPayment{
		Account: payment.Account, Destination: payment.Destination, Amount: payment.Amount,
		Fee: payment.Fee, Sequence: payment.Sequence, LastLedgerSequence: payment.LastLedgerSequence,
		SigningPubKey: payment.SigningPubKey,
	}.serialize(true, false)
	if err != nil {
		return fmt.Errorf("xrp verify: %w", err)
	}
	if !sig.Verify(xrpSHA512Half(xrpSignPrefix, preimage), pub) {
		return fmt.Errorf("xrp verify: signature does not match the unsigned payment")
	}
	return nil
}

func (a *Live) requireAltnet(ctx context.Context) error {
	if a == nil || !a.cfg.IsTestnet {
		return fmt.Errorf("xrp payments require the altnet testnet adapter")
	}
	if err := refuseMainnetEndpoint(a.endpoint()); err != nil {
		return err
	}
	facts, err := a.ledgerFacts(ctx)
	if err != nil {
		return err
	}
	if facts.NetworkID != altnetNetworkID {
		return fmt.Errorf("xrp rpc network_id is %d, want altnet %d", facts.NetworkID, altnetNetworkID)
	}
	return nil
}

func refuseMainnetEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("xrp rpc url is not usable")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if _, denied := xrpMainnetHosts[host]; denied || strings.HasSuffix(host, ".ripple.com") {
		return fmt.Errorf("xrp rpc host %s is not the altnet", host)
	}
	return nil
}

func xrpReserveAllows(balance, amount, fee, reserve *big.Int) error {
	if reserve == nil || reserve.Sign() <= 0 {
		return fmt.Errorf("xrp reserve is missing")
	}
	left := new(big.Int).Sub(balance, amount)
	left.Sub(left, fee)
	if left.Cmp(reserve) < 0 {
		return fmt.Errorf("xrp payment would leave %s drops, below the %s-drop reserve", left, reserve)
	}
	return nil
}

func xrpSubmitAccepted(engine string) bool {
	switch {
	case strings.HasPrefix(engine, "tes"):
		return true
	case engine == "terQUEUED":
		return true
	default:
		return false
	}
}

func (p xrpPayment) serialize(includePub, includeSignature bool) ([]byte, error) {
	if len(p.Account) != 20 || len(p.Destination) != 20 {
		return nil, fmt.Errorf("xrp payment accounts must be 20 bytes")
	}
	if p.Amount > xrpAmountMax || p.Fee > xrpAmountMax {
		return nil, fmt.Errorf("xrp amount does not fit a native drops field")
	}
	if includePub && len(p.SigningPubKey) != 33 {
		return nil, fmt.Errorf("xrp signing public key must be 33 bytes")
	}
	if includeSignature && !includePub {
		return nil, fmt.Errorf("xrp signature requires the signing public key")
	}
	var out []byte
	out = appendField(out, xrpTypeUInt16, xrpFieldTransactionType, putU16(xrpPaymentType))
	out = appendField(out, xrpTypeUInt32, xrpFieldSequence, putU32(p.Sequence))
	out = appendField(out, xrpTypeUInt32, xrpFieldLastLedgerSequence, putU32(p.LastLedgerSequence))
	out = appendField(out, xrpTypeAmount, xrpFieldAmount, putXRP(p.Amount))
	out = appendField(out, xrpTypeAmount, xrpFieldFee, putXRP(p.Fee))
	if includePub {
		pub, err := putVL(p.SigningPubKey)
		if err != nil {
			return nil, err
		}
		out = appendField(out, xrpTypeVL, xrpFieldSigningPubKey, pub)
	}
	if includeSignature {
		sig, err := putVL(p.TxnSignature)
		if err != nil {
			return nil, err
		}
		out = appendField(out, xrpTypeVL, xrpFieldTxnSignature, sig)
	}
	account, err := putVL(p.Account)
	if err != nil {
		return nil, err
	}
	destination, err := putVL(p.Destination)
	if err != nil {
		return nil, err
	}
	out = appendField(out, xrpTypeAccount, xrpFieldAccount, account)
	out = appendField(out, xrpTypeAccount, xrpFieldDestination, destination)
	return out, nil
}

func parseXRPPayment(blob []byte) (xrpPayment, error) {
	var payment xrpPayment
	rest := blob
	var err error
	for len(rest) > 0 {
		var typ, nth int
		var payload []byte
		typ, nth, payload, rest, err = readXRPField(rest)
		if err != nil {
			return xrpPayment{}, err
		}
		switch {
		case typ == xrpTypeUInt16 && nth == xrpFieldTransactionType:
			if binary.BigEndian.Uint16(payload) != xrpPaymentType {
				return xrpPayment{}, fmt.Errorf("not a payment")
			}
		case typ == xrpTypeUInt32 && nth == xrpFieldSequence:
			payment.Sequence = binary.BigEndian.Uint32(payload)
		case typ == xrpTypeUInt32 && nth == xrpFieldLastLedgerSequence:
			payment.LastLedgerSequence = binary.BigEndian.Uint32(payload)
		case typ == xrpTypeAmount && nth == xrpFieldAmount:
			payment.Amount, err = takeXRP(payload)
		case typ == xrpTypeAmount && nth == xrpFieldFee:
			payment.Fee, err = takeXRP(payload)
		case typ == xrpTypeVL && nth == xrpFieldSigningPubKey:
			payment.SigningPubKey = append([]byte(nil), payload...)
		case typ == xrpTypeVL && nth == xrpFieldTxnSignature:
			payment.TxnSignature = append([]byte(nil), payload...)
		case typ == xrpTypeAccount && nth == xrpFieldAccount:
			payment.Account = append([]byte(nil), payload...)
		case typ == xrpTypeAccount && nth == xrpFieldDestination:
			payment.Destination = append([]byte(nil), payload...)
		default:
			return xrpPayment{}, fmt.Errorf("unexpected field type %d id %d", typ, nth)
		}
		if err != nil {
			return xrpPayment{}, err
		}
	}
	if len(payment.Account) != 20 || len(payment.Destination) != 20 {
		return xrpPayment{}, fmt.Errorf("payment is missing account or destination")
	}
	if len(payment.SigningPubKey) != 0 && len(payment.SigningPubKey) != 33 {
		return xrpPayment{}, fmt.Errorf("signing public key must be 33 bytes")
	}
	return payment, nil
}

func readXRPField(in []byte) (typ, nth int, payload, rest []byte, err error) {
	if len(in) == 0 {
		return 0, 0, nil, nil, fmt.Errorf("truncated field header")
	}
	first := in[0]
	in = in[1:]
	switch {
	case first == 0:
		if len(in) < 2 {
			return 0, 0, nil, nil, fmt.Errorf("truncated field header")
		}
		typ, nth, in = int(in[0]), int(in[1]), in[2:]
	case first>>4 == 0:
		if len(in) < 1 {
			return 0, 0, nil, nil, fmt.Errorf("truncated field header")
		}
		nth, typ, in = int(first), int(in[0]), in[1:]
	case first&0x0f == 0:
		if len(in) < 1 {
			return 0, 0, nil, nil, fmt.Errorf("truncated field header")
		}
		typ, nth, in = int(first>>4), int(in[0]), in[1:]
	default:
		typ, nth = int(first>>4), int(first&0x0f)
	}
	switch typ {
	case xrpTypeUInt16:
		if len(in) < 2 {
			return 0, 0, nil, nil, fmt.Errorf("truncated uint16")
		}
		return typ, nth, in[:2], in[2:], nil
	case xrpTypeUInt32:
		if len(in) < 4 {
			return 0, 0, nil, nil, fmt.Errorf("truncated uint32")
		}
		return typ, nth, in[:4], in[4:], nil
	case xrpTypeAmount:
		if len(in) < 8 {
			return 0, 0, nil, nil, fmt.Errorf("truncated amount")
		}
		return typ, nth, in[:8], in[8:], nil
	case xrpTypeVL, xrpTypeAccount:
		value, rest, err := takeVL(in)
		if err != nil {
			return 0, 0, nil, nil, err
		}
		return typ, nth, value, rest, nil
	default:
		return 0, 0, nil, nil, fmt.Errorf("unsupported field type %d", typ)
	}
}

func appendField(out []byte, typ, nth int, payload []byte) []byte {
	return append(append(out, fieldHeader(typ, nth)...), payload...)
}

func fieldHeader(typ, nth int) []byte {
	switch {
	case typ < 16 && nth < 16:
		return []byte{byte(typ<<4 | nth)}
	case typ < 16 && nth >= 16:
		return []byte{byte(typ << 4), byte(nth)}
	case typ >= 16 && nth < 16:
		return []byte{byte(nth), byte(typ)}
	default:
		return []byte{0, byte(typ), byte(nth)}
	}
}

func putU16(v uint16) []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, v)
	return out
}

func putU32(v uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, v)
	return out
}

func putXRP(drops uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, drops|xrpAmountPositive)
	return out
}

func takeXRP(raw []byte) (uint64, error) {
	if len(raw) != 8 {
		return 0, fmt.Errorf("xrp amount must be 8 bytes")
	}
	value := binary.BigEndian.Uint64(raw)
	if value&0x8000000000000000 != 0 || value&xrpAmountPositive == 0 {
		return 0, fmt.Errorf("amount is not positive native XRP")
	}
	return value & xrpAmountMax, nil
}

func putVL(value []byte) ([]byte, error) {
	n := len(value)
	var prefix []byte
	switch {
	case n <= 192:
		prefix = []byte{byte(n)}
	case n <= 12480:
		adjusted := n - 193
		prefix = []byte{byte(193 + (adjusted >> 8)), byte(adjusted)}
	default:
		return nil, fmt.Errorf("xrp variable field is %d bytes", n)
	}
	return append(prefix, value...), nil
}

func takeVL(in []byte) ([]byte, []byte, error) {
	if len(in) == 0 {
		return nil, nil, fmt.Errorf("truncated variable length")
	}
	first := int(in[0])
	var n, width int
	switch {
	case first <= 192:
		n, width = first, 1
	case first <= 240:
		if len(in) < 2 {
			return nil, nil, fmt.Errorf("truncated variable length")
		}
		n, width = 193+((first-193)<<8)+int(in[1]), 2
	default:
		return nil, nil, fmt.Errorf("xrp variable field length is too wide")
	}
	if len(in) < width+n {
		return nil, nil, fmt.Errorf("truncated variable field")
	}
	return in[width : width+n], in[width+n:], nil
}

func compressedPub(privateKey []byte) []byte {
	priv, _ := btcec.PrivKeyFromBytes(privateKey)
	return priv.PubKey().SerializeCompressed()
}

func xrpSHA512Half(prefix, payload []byte) []byte {
	sum := sha512.Sum512(append(append([]byte{}, prefix...), payload...))
	return sum[:32]
}

func metadataString(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return value
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
