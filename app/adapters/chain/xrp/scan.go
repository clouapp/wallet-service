package xrp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	xrpPaymentTransactionType = "Payment"
	// xrpDeliveredUnavailable is delivered_amount on a Payment validated before
	// 2014-01-20, when the ledger did not record what arrived.
	xrpDeliveredUnavailable = "unavailable"
	// xrpLedgerMaxResponseBytes is the most one ledger answer with its expanded
	// transactions may be (a busy mainnet ledger is a few MB). Past this the read
	// fails instead of truncating.
	xrpLedgerMaxResponseBytes = 32 << 20
	// rippleEpochUnix is the Ripple epoch, 2000-01-01T00:00:00Z, in Unix seconds:
	// ledger close times count from it.
	rippleEpochUnix = 946684800
)

// xrpLedgerAnswer is the ledger method with transactions and expand true.
// ledger.ledger_index is a string in API v1 and a number in API v2.
type xrpLedgerAnswer struct {
	xrpRPCError
	Validated bool `json:"validated"`
	Ledger    struct {
		Closed       bool               `json:"closed"`
		LedgerIndex  json.Number        `json:"ledger_index"`
		LedgerHash   string             `json:"ledger_hash"`
		CloseTime    json.Number        `json:"close_time"`
		Transactions *[]json.RawMessage `json:"transactions"`
	} `json:"ledger"`
}

// xrpLedgerTxFields are the transaction fields a deposit is read from.
type xrpLedgerTxFields struct {
	TransactionType string  `json:"TransactionType"`
	Account         string  `json:"Account"`
	Destination     string  `json:"Destination"`
	DestinationTag  *uint32 `json:"DestinationTag"`
}

// xrpLedgerTxMeta is the part of a transaction's metadata a deposit is read from.
// delivered_amount is what a Payment delivered: drops as a string, or an object
// for an issued currency.
type xrpLedgerTxMeta struct {
	TransactionResult string          `json:"TransactionResult"`
	TransactionIndex  json.Number     `json:"TransactionIndex"`
	DeliveredAmount   json.RawMessage `json:"delivered_amount"`
}

// xrpLedgerTx is one expanded transaction of a ledger. API v1 puts the fields at
// the top level next to metaData; API v2 nests them in tx_json next to meta.
type xrpLedgerTx struct {
	xrpLedgerTxFields
	Hash     string             `json:"hash"`
	TxJSON   *xrpLedgerTxFields `json:"tx_json"`
	Meta     *xrpLedgerTxMeta   `json:"meta"`
	MetaData *xrpLedgerTxMeta   `json:"metaData"`
}

func (tx xrpLedgerTx) fields() xrpLedgerTxFields {
	if tx.TxJSON != nil {
		return *tx.TxJSON
	}
	return tx.xrpLedgerTxFields
}

func (tx xrpLedgerTx) meta() *xrpLedgerTxMeta {
	if tx.Meta != nil {
		return tx.Meta
	}
	return tx.MetaData
}

// xrpLedgerEntry is a transaction of a scanned ledger with its position in it.
type xrpLedgerEntry struct {
	hash   string
	index  uint64
	fields xrpLedgerTxFields
	meta   xrpLedgerTxMeta
}

// xrpScannedLedger is one validated, closed ledger and its transactions in ledger
// order.
type xrpScannedLedger struct {
	index     uint64
	hash      string
	closeTime time.Time
	entries   []xrpLedgerEntry
}

// ScanBlock returns the deposits of validated ledger ledgerIndex: every Payment
// whose result is tesSUCCESS and that delivered XRP to another account, for the
// drops meta.delivered_amount reports (a partial payment counts what arrived, not
// what it asked for), with its destination tag. Issued currencies, failed payments,
// payments an account makes to itself and every other transaction type are not
// deposits. A ledger the server cannot show validated, closed and complete is an
// error, never an empty ledger, so the scanner retries it.
func (a *Live) ScanBlock(ctx context.Context, ledgerIndex uint64) ([]types.DetectedTransfer, error) {
	ledger, err := a.validatedLedger(ctx, ledgerIndex)
	if err != nil {
		return nil, fmt.Errorf("scan xrp ledger %d: %w", ledgerIndex, err)
	}
	transfers, err := ledger.deposits(a.cfg.NativeSymbol)
	if err != nil {
		return nil, fmt.Errorf("scan xrp ledger %d: %w", ledgerIndex, err)
	}
	return transfers, nil
}

// validatedLedger reads ledgerIndex with its expanded transactions and refuses an
// answer that is not that ledger, validated and closed, with every transaction
// expanded and its metadata.
func (a *Live) validatedLedger(ctx context.Context, ledgerIndex uint64) (xrpScannedLedger, error) {
	var answer xrpLedgerAnswer
	err := a.callWithLimit(ctx, "ledger", []any{map[string]any{
		"ledger_index": ledgerIndex,
		"transactions": true,
		"expand":       true,
	}}, &answer, xrpLedgerMaxResponseBytes)
	if err != nil {
		return xrpScannedLedger{}, err
	}
	if answer.Status != "success" {
		return xrpScannedLedger{}, fmt.Errorf("xrp ledger: %s", xrpErrorCode(answer.xrpRPCError))
	}
	if !answer.Validated {
		return xrpScannedLedger{}, fmt.Errorf("ledger is not validated yet")
	}
	answered, err := nonNegativeJSON(answer.Ledger.LedgerIndex)
	if err != nil {
		return xrpScannedLedger{}, fmt.Errorf("ledger index: %w", err)
	}
	if answered != ledgerIndex {
		return xrpScannedLedger{}, fmt.Errorf("the server answered ledger %d", answered)
	}
	if !answer.Ledger.Closed {
		return xrpScannedLedger{}, fmt.Errorf("ledger is validated but not closed")
	}
	if answer.Ledger.Transactions == nil {
		return xrpScannedLedger{}, fmt.Errorf("the server returned no transactions list")
	}
	closeTime, err := ledgerCloseTime(answer.Ledger.CloseTime)
	if err != nil {
		return xrpScannedLedger{}, err
	}
	ledger := xrpScannedLedger{index: ledgerIndex, hash: answer.Ledger.LedgerHash, closeTime: closeTime}
	for _, raw := range *answer.Ledger.Transactions {
		entry, err := parseLedgerEntry(raw)
		if err != nil {
			return xrpScannedLedger{}, err
		}
		ledger.entries = append(ledger.entries, entry)
	}
	sort.SliceStable(ledger.entries, func(i, j int) bool { return ledger.entries[i].index < ledger.entries[j].index })
	return ledger, nil
}

// ledgerCloseTime reads close_time, seconds since the Ripple epoch. A missing one
// is the zero time: the close time is not what a deposit is matched on.
func ledgerCloseTime(number json.Number) (time.Time, error) {
	if number == "" {
		return time.Time{}, nil
	}
	seconds, err := number.Int64()
	if err != nil || seconds < 0 || seconds > math.MaxInt64-rippleEpochUnix {
		return time.Time{}, fmt.Errorf("ledger close time %s is not a number of seconds", number)
	}
	return time.Unix(seconds+rippleEpochUnix, 0).UTC(), nil
}

func parseLedgerEntry(raw json.RawMessage) (xrpLedgerEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return xrpLedgerEntry{}, fmt.Errorf("transactions are not expanded")
	}
	var tx xrpLedgerTx
	if err := json.Unmarshal(trimmed, &tx); err != nil {
		return xrpLedgerEntry{}, fmt.Errorf("transaction is not the expected shape")
	}
	if tx.Hash == "" {
		return xrpLedgerEntry{}, fmt.Errorf("transaction without a hash")
	}
	hash := strings.ToUpper(tx.Hash)
	meta := tx.meta()
	if meta == nil || meta.TransactionResult == "" {
		return xrpLedgerEntry{}, fmt.Errorf("transaction %s has no metadata", hash)
	}
	index, err := nonNegativeJSON(meta.TransactionIndex)
	if err != nil {
		return xrpLedgerEntry{}, fmt.Errorf("transaction %s index: %w", hash, err)
	}
	return xrpLedgerEntry{hash: hash, index: index, fields: tx.fields(), meta: *meta}, nil
}

// deposits returns the ledger's successful native payments to another account,
// in ledger order.
func (l xrpScannedLedger) deposits(asset string) ([]types.DetectedTransfer, error) {
	var transfers []types.DetectedTransfer
	for _, entry := range l.entries {
		if entry.fields.TransactionType != xrpPaymentTransactionType || entry.meta.TransactionResult != xrpSuccessResult {
			continue
		}
		drops, native, err := deliveredDrops(entry.meta.DeliveredAmount)
		if err != nil {
			return nil, fmt.Errorf("payment %s delivered_amount: %w", entry.hash, err)
		}
		if !native || drops.Sign() == 0 {
			continue
		}
		if entry.fields.Destination == "" || entry.fields.Account == "" {
			return nil, fmt.Errorf("payment %s has no account or destination", entry.hash)
		}
		// A payment to itself converts the account's own currencies; nothing arrives
		// from anyone else.
		if entry.fields.Account == entry.fields.Destination {
			continue
		}
		transfer := types.DetectedTransfer{
			TxHash:      entry.hash,
			BlockNumber: l.index,
			BlockHash:   l.hash,
			From:        entry.fields.Account,
			To:          entry.fields.Destination,
			Amount:      drops,
			Asset:       asset,
			// A transaction is one Payment: what it delivers is its native value.
			LogIndex:  types.NativeValuePosition,
			Timestamp: l.closeTime,
		}
		if tag := entry.fields.DestinationTag; tag != nil {
			value := *tag
			transfer.DestinationTag = &value
		}
		transfers = append(transfers, transfer)
	}
	return transfers, nil
}

// deliveredDrops reads delivered_amount: drops when it is XRP (a decimal string),
// native false for an issued currency (an object). A missing or unavailable amount
// is an error: what arrived is never guessed from what was asked.
func deliveredDrops(raw json.RawMessage) (*big.Int, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, fmt.Errorf("missing")
	}
	if trimmed[0] == '{' {
		return nil, false, nil
	}
	if string(trimmed) == `"`+xrpDeliveredUnavailable+`"` {
		return nil, false, fmt.Errorf("unavailable")
	}
	drops, err := parseDrops(trimmed)
	if err != nil {
		return nil, false, err
	}
	return drops, true, nil
}
