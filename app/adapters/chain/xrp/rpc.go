package xrp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/app/services/chain"
)

const (
	xrpAccountNotFound = "actNotFound"
	xrpSuccessResult   = "tesSUCCESS"
	// xrpRequireDestTagFlag is lsfRequireDestTag on an AccountRoot.
	xrpRequireDestTagFlag = 0x00020000
	// The reserves used only when the server reports none: 1 XRP base and 0.2 XRP
	// per owned object, the mainnet and altnet values since December 2024.
	xrpFallbackBaseReserveDrops  = 1_000_000
	xrpFallbackOwnerReserveDrops = 200_000
)

type xrpRPCRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

type xrpRPCError struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (a *Live) accountDrops(ctx context.Context, address string) (*big.Int, error) {
	var result struct {
		xrpRPCError
		AccountData struct {
			Balance json.RawMessage `json:"Balance"`
		} `json:"account_data"`
	}
	err := a.call(ctx, "account_info", []any{map[string]string{
		"account":      address,
		"ledger_index": "validated",
	}}, &result)
	if err != nil {
		return nil, err
	}
	if result.Status == "error" && result.Error == xrpAccountNotFound {
		return new(big.Int), nil
	}
	if result.Status != "success" {
		return nil, fmt.Errorf("xrp account_info: %s", xrpErrorCode(result.xrpRPCError))
	}
	drops, err := parseDrops(result.AccountData.Balance)
	if err != nil {
		return nil, fmt.Errorf("xrp account_info balance: %w", err)
	}
	return drops, nil
}

// xrpAccountState is an AccountRoot on one ledger. A missing account has Exists
// false and a zero balance.
type xrpAccountState struct {
	Exists     bool
	Balance    *big.Int
	Sequence   uint64
	OwnerCount uint64
	Flags      uint64
}

func (s xrpAccountState) requiresDestinationTag() bool {
	return s.Flags&xrpRequireDestTagFlag != 0
}

// accountState reads address on ledgerIndex ("validated" or "current"; the current
// open ledger already counts payments not validated yet).
func (a *Live) accountState(ctx context.Context, address, ledgerIndex string) (xrpAccountState, error) {
	var result struct {
		xrpRPCError
		AccountData struct {
			Balance    json.RawMessage `json:"Balance"`
			Sequence   json.Number     `json:"Sequence"`
			OwnerCount json.Number     `json:"OwnerCount"`
			Flags      json.Number     `json:"Flags"`
		} `json:"account_data"`
	}
	err := a.call(ctx, "account_info", []any{map[string]string{
		"account":      address,
		"ledger_index": ledgerIndex,
	}}, &result)
	if err != nil {
		return xrpAccountState{}, err
	}
	if result.Status == "error" && result.Error == xrpAccountNotFound {
		return xrpAccountState{Balance: new(big.Int)}, nil
	}
	if result.Status != "success" {
		return xrpAccountState{}, fmt.Errorf("xrp account_info: %s", xrpErrorCode(result.xrpRPCError))
	}
	state := xrpAccountState{Exists: true}
	if state.Balance, err = parseDrops(result.AccountData.Balance); err != nil {
		return xrpAccountState{}, fmt.Errorf("xrp account_info balance: %w", err)
	}
	if state.Sequence, err = nonNegativeJSON(result.AccountData.Sequence); err != nil {
		return xrpAccountState{}, fmt.Errorf("xrp account_info sequence: %w", err)
	}
	if state.OwnerCount, err = optionalNonNegativeJSON(result.AccountData.OwnerCount); err != nil {
		return xrpAccountState{}, fmt.Errorf("xrp account_info owner count: %w", err)
	}
	if state.Flags, err = optionalNonNegativeJSON(result.AccountData.Flags); err != nil {
		return xrpAccountState{}, fmt.Errorf("xrp account_info flags: %w", err)
	}
	return state, nil
}

func (a *Live) openLedgerFeeDrops(ctx context.Context) (*big.Int, error) {
	var result struct {
		xrpRPCError
		Drops struct {
			OpenLedgerFee json.RawMessage `json:"open_ledger_fee"`
			MinimumFee    json.RawMessage `json:"minimum_fee"`
		} `json:"drops"`
	}
	err := a.call(ctx, "fee", []any{map[string]any{}}, &result)
	if err != nil {
		return nil, err
	}
	if result.Status != "success" {
		return nil, fmt.Errorf("xrp fee: %s", xrpErrorCode(result.xrpRPCError))
	}
	fee, err := parseDrops(result.Drops.OpenLedgerFee)
	if err != nil {
		return nil, fmt.Errorf("xrp open ledger fee: %w", err)
	}
	if fee.Sign() == 0 {
		fee, err = parseDrops(result.Drops.MinimumFee)
		if err != nil || fee.Sign() == 0 {
			return nil, fmt.Errorf("xrp fee did not return a positive open ledger fee")
		}
	}
	return fee, nil
}

type xrpLedgerFacts struct {
	NetworkID uint64
	// ReserveDrops is the base reserve; ReserveIncDrops the owner reserve per object.
	ReserveDrops    *big.Int
	ReserveIncDrops *big.Int
	LedgerIndex     uint64
}

// accountReserve is what an account holding ownerCount ledger objects must keep.
func (f xrpLedgerFacts) accountReserve(ownerCount uint64) *big.Int {
	reserve := new(big.Int).Mul(f.ReserveIncDrops, new(big.Int).SetUint64(ownerCount))
	return reserve.Add(reserve, f.ReserveDrops)
}

func (a *Live) ledgerFacts(ctx context.Context) (xrpLedgerFacts, error) {
	var result struct {
		xrpRPCError
		Info struct {
			NetworkID       json.Number `json:"network_id"`
			ValidatedLedger struct {
				ReserveBase json.RawMessage `json:"reserve_base_xrp"`
				ReserveInc  json.RawMessage `json:"reserve_inc_xrp"`
				Seq         json.Number     `json:"seq"`
			} `json:"validated_ledger"`
		} `json:"info"`
	}
	err := a.call(ctx, "server_info", []any{map[string]any{}}, &result)
	if err != nil {
		return xrpLedgerFacts{}, err
	}
	if result.Status != "success" {
		return xrpLedgerFacts{}, fmt.Errorf("xrp server_info: %s", xrpErrorCode(result.xrpRPCError))
	}
	networkID, err := nonNegativeJSON(result.Info.NetworkID)
	if err != nil {
		return xrpLedgerFacts{}, fmt.Errorf("xrp server_info network_id: %w", err)
	}
	ledger, err := nonNegativeJSON(result.Info.ValidatedLedger.Seq)
	if err != nil {
		return xrpLedgerFacts{}, fmt.Errorf("xrp server_info ledger: %w", err)
	}
	validated := result.Info.ValidatedLedger
	base, inc, err := a.reserves(ctx, validated.ReserveBase, validated.ReserveInc)
	if err != nil {
		return xrpLedgerFacts{}, err
	}
	return xrpLedgerFacts{NetworkID: networkID, ReserveDrops: base, ReserveIncDrops: inc, LedgerIndex: ledger}, nil
}

// reserves reads the base and owner reserves from server_info (whole XRP). When
// server_info omits them, server_state (drops) answers; when it cannot either, the
// current network values are used and a warning is logged. A reserve the server
// does report but that does not parse is an error, not a fallback.
func (a *Live) reserves(ctx context.Context, baseRaw, incRaw json.RawMessage) (*big.Int, *big.Int, error) {
	if len(baseRaw) > 0 && len(incRaw) > 0 {
		base, err := xrpReserveDrops(baseRaw)
		if err != nil {
			return nil, nil, err
		}
		inc, err := xrpReserveDrops(incRaw)
		if err != nil {
			return nil, nil, err
		}
		return base, inc, nil
	}
	base, inc, err := a.serverStateReserves(ctx)
	if err == nil {
		return base, inc, nil
	}
	slog.Warn("xrp reserves not reported by the server; using the fallback reserves",
		"base_drops", xrpFallbackBaseReserveDrops, "owner_drops", xrpFallbackOwnerReserveDrops, "error", err)
	return big.NewInt(xrpFallbackBaseReserveDrops), big.NewInt(xrpFallbackOwnerReserveDrops), nil
}

func (a *Live) serverStateReserves(ctx context.Context) (*big.Int, *big.Int, error) {
	var result struct {
		xrpRPCError
		State struct {
			ValidatedLedger struct {
				ReserveBase json.Number `json:"reserve_base"`
				ReserveInc  json.Number `json:"reserve_inc"`
			} `json:"validated_ledger"`
		} `json:"state"`
	}
	if err := a.call(ctx, "server_state", []any{map[string]any{}}, &result); err != nil {
		return nil, nil, err
	}
	if result.Status != "success" {
		return nil, nil, fmt.Errorf("xrp server_state: %s", xrpErrorCode(result.xrpRPCError))
	}
	base, err := nonNegativeJSON(result.State.ValidatedLedger.ReserveBase)
	if err != nil || base == 0 {
		return nil, nil, fmt.Errorf("xrp server_state reserve_base is not a positive number of drops")
	}
	inc, err := nonNegativeJSON(result.State.ValidatedLedger.ReserveInc)
	if err != nil {
		return nil, nil, fmt.Errorf("xrp server_state reserve_inc: %w", err)
	}
	return new(big.Int).SetUint64(base), new(big.Int).SetUint64(inc), nil
}

type xrpTxResult struct {
	xrpRPCError
	Validated   bool            `json:"validated"`
	LedgerIndex json.Number     `json:"ledger_index"`
	Hash        string          `json:"hash"`
	Fee         json.RawMessage `json:"Fee"`
	TxJSON      struct {
		Fee json.RawMessage `json:"Fee"`
	} `json:"tx_json"`
	Meta struct {
		TransactionResult string `json:"TransactionResult"`
	} `json:"meta"`
}

// lookupTransaction reads txHash with the tx method. found is false while no server
// knows the transaction yet.
func (a *Live) lookupTransaction(ctx context.Context, txHash string) (xrpTxResult, bool, error) {
	if txHash == "" {
		return xrpTxResult{}, false, fmt.Errorf("xrp tx hash is required")
	}
	var result xrpTxResult
	err := a.call(ctx, "tx", []any{map[string]any{"transaction": txHash, "binary": false}}, &result)
	if err != nil {
		return xrpTxResult{}, false, err
	}
	if result.Status == "error" && result.Error == "txnNotFound" {
		return xrpTxResult{}, false, nil
	}
	if result.Status != "success" {
		return xrpTxResult{}, false, fmt.Errorf("xrp tx: %s", xrpErrorCode(result.xrpRPCError))
	}
	if result.Hash != "" && !strings.EqualFold(result.Hash, txHash) {
		return xrpTxResult{}, false, fmt.Errorf("xrp tx %s answered for %s", txHash, result.Hash)
	}
	return result, true, nil
}

// transactionLedger is the validated ledger of a successful transaction, or 0 while
// it is not in a validated ledger. A validated transaction whose result is not
// tesSUCCESS is chain.ErrTransactionFailed, so it is never counted as confirmed.
func (a *Live) transactionLedger(ctx context.Context, txHash string) (uint64, error) {
	result, found, err := a.lookupTransaction(ctx, txHash)
	if err != nil || !found || !result.Validated {
		return 0, err
	}
	ledger, err := nonNegativeJSON(result.LedgerIndex)
	if err != nil {
		return 0, fmt.Errorf("xrp tx ledger: %w", err)
	}
	if outcome := result.Meta.TransactionResult; outcome != xrpSuccessResult {
		if outcome == "" {
			return 0, fmt.Errorf("xrp tx %s is validated without a TransactionResult", txHash)
		}
		return 0, fmt.Errorf("xrp tx %s: %w", txHash, &chain.TransactionFailedError{Result: outcome, Block: ledger})
	}
	return ledger, nil
}

var _ chain.TransactionExpiryChecker = (*Live)(nil)

// TransactionExpired reports that txHash can never be included: the validated ledger
// is past lastLedger (its LastLedgerSequence), the server holds every ledger from the
// one it was built on to lastLedger (complete_ledgers), and the tx method searched
// that whole range without finding it (searched_all). Anything less is "not yet".
func (a *Live) TransactionExpired(ctx context.Context, txHash string, lastLedger uint64) (bool, error) {
	if txHash == "" || lastLedger == 0 {
		return false, fmt.Errorf("xrp expiry: tx hash and last ledger are required")
	}
	minLedger := uint64(1)
	if lastLedger > xrpLastLedgerWindow {
		minLedger = lastLedger - xrpLastLedgerWindow
	}
	validated, complete, err := a.completeLedgers(ctx)
	if err != nil {
		return false, err
	}
	if validated <= lastLedger || !complete.covers(minLedger, lastLedger) {
		return false, nil
	}
	var result struct {
		xrpRPCError
		Validated   bool `json:"validated"`
		SearchedAll bool `json:"searched_all"`
	}
	err = a.call(ctx, "tx", []any{map[string]any{
		"transaction": txHash, "binary": false, "min_ledger": minLedger, "max_ledger": lastLedger,
	}}, &result)
	if err != nil {
		return false, err
	}
	if result.Status == "error" && result.Error == "txnNotFound" {
		return result.SearchedAll, nil
	}
	if result.Status != "success" {
		return false, fmt.Errorf("xrp tx: %s", xrpErrorCode(result.xrpRPCError))
	}
	return false, nil
}

// ledgerRanges is server_info complete_ledgers, e.g. "32570-62000000,62000002-62000100".
type ledgerRanges [][2]uint64

func (r ledgerRanges) covers(from, to uint64) bool {
	for _, span := range r {
		if span[0] <= from && to <= span[1] {
			return true
		}
	}
	return false
}

func (a *Live) completeLedgers(ctx context.Context) (uint64, ledgerRanges, error) {
	var result struct {
		xrpRPCError
		Info struct {
			CompleteLedgers string `json:"complete_ledgers"`
			ValidatedLedger struct {
				Seq json.Number `json:"seq"`
			} `json:"validated_ledger"`
		} `json:"info"`
	}
	if err := a.call(ctx, "server_info", []any{map[string]any{}}, &result); err != nil {
		return 0, nil, err
	}
	if result.Status != "success" {
		return 0, nil, fmt.Errorf("xrp server_info: %s", xrpErrorCode(result.xrpRPCError))
	}
	validated, err := nonNegativeJSON(result.Info.ValidatedLedger.Seq)
	if err != nil {
		return 0, nil, fmt.Errorf("xrp server_info ledger: %w", err)
	}
	return validated, parseLedgerRanges(result.Info.CompleteLedgers), nil
}

// parseLedgerRanges reads complete_ledgers; "empty" or anything unreadable is no range.
func parseLedgerRanges(text string) ledgerRanges {
	var ranges ledgerRanges
	for _, part := range strings.Split(strings.TrimSpace(text), ",") {
		bounds := strings.SplitN(strings.TrimSpace(part), "-", 2)
		low, err := strconv.ParseUint(bounds[0], 10, 64)
		if err != nil {
			continue
		}
		high := low
		if len(bounds) == 2 {
			if high, err = strconv.ParseUint(bounds[1], 10, 64); err != nil || high < low {
				continue
			}
		}
		ranges = append(ranges, [2]uint64{low, high})
	}
	return ranges
}

// optionalNonNegativeJSON is nonNegativeJSON with a missing field read as 0.
func optionalNonNegativeJSON(number json.Number) (uint64, error) {
	if number == "" {
		return 0, nil
	}
	return nonNegativeJSON(number)
}

func nonNegativeJSON(number json.Number) (uint64, error) {
	if number == "" {
		return 0, fmt.Errorf("missing integer")
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s is not a non-negative integer", number)
	}
	return uint64(parsed), nil
}

// xrpReserveDrops converts reserve_base_xrp (whole XRP, or a decimal string)
// into drops. A JSON number is accepted here because rippled sends the reserve
// as a number of XRP, not as drops.
func xrpReserveDrops(raw json.RawMessage) (*big.Int, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("xrp reserve is missing")
	}
	text := strings.Trim(string(raw), `"`)
	if text == "" || strings.ContainsAny(text, "eE+") {
		return nil, fmt.Errorf("xrp reserve %s is not a decimal amount of XRP", raw)
	}
	rat, ok := new(big.Rat).SetString(text)
	if !ok || rat.Sign() <= 0 {
		return nil, fmt.Errorf("xrp reserve %s is not a positive amount of XRP", raw)
	}
	rat.Mul(rat, big.NewRat(DropsPerXRP, 1))
	if !rat.IsInt() {
		return nil, fmt.Errorf("xrp reserve %s is not a whole number of drops", raw)
	}
	return rat.Num(), nil
}

func (a *Live) validatedLedgerIndex(ctx context.Context) (uint64, error) {
	var result struct {
		xrpRPCError
		LedgerIndex json.Number `json:"ledger_index"`
		Ledger      struct {
			LedgerIndex json.Number `json:"ledger_index"`
		} `json:"ledger"`
	}
	err := a.call(ctx, "ledger", []any{map[string]any{
		"ledger_index": "validated",
		"transactions": false,
		"expand":       false,
	}}, &result)
	if err != nil {
		return 0, err
	}
	if result.Status != "success" {
		return 0, fmt.Errorf("xrp ledger: %s", xrpErrorCode(result.xrpRPCError))
	}
	index := result.Ledger.LedgerIndex
	if index == "" {
		index = result.LedgerIndex
	}
	parsed, err := index.Int64()
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("xrp ledger index is not a non-negative integer")
	}
	return uint64(parsed), nil
}

func (a *Live) call(ctx context.Context, method string, params []any, dest any) error {
	body, err := json.Marshal(xrpRPCRequest{Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("xrp %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("xrp %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xrp %s: http %d", method, resp.StatusCode)
	}
	raw, err := readLimited(resp.Body, xrpRPCMaxResponseBytes)
	if err != nil {
		return fmt.Errorf("xrp %s: %w", method, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("xrp %s: response is not json", method)
	}
	if len(envelope.Result) == 0 {
		return fmt.Errorf("xrp %s: response has no result", method)
	}
	inner := json.NewDecoder(bytes.NewReader(envelope.Result))
	inner.UseNumber()
	if err := inner.Decode(dest); err != nil {
		return fmt.Errorf("xrp %s: result is not the expected shape", method)
	}
	return nil
}

func readLimited(body io.Reader, limit int) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("response larger than %d bytes", limit)
	}
	return raw, nil
}

// parseDrops accepts a JSON string of an integer. A JSON number is refused so
// a float cannot be mistaken for drops.
func parseDrops(raw json.RawMessage) (*big.Int, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return nil, fmt.Errorf("drops must be a decimal string, not %s", dropsKind(raw))
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, err
	}
	if text == "" || strings.HasPrefix(text, "+") || strings.ContainsAny(text, ".eE") {
		return nil, fmt.Errorf("drops %q is not an integer", text)
	}
	drops, ok := new(big.Int).SetString(text, 10)
	if !ok || drops.Sign() < 0 {
		return nil, fmt.Errorf("drops %q is not a non-negative integer", text)
	}
	return drops, nil
}

func dropsKind(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "empty"
	}
	if raw[0] == '"' {
		return "a string"
	}
	return "a number"
}

func xrpErrorCode(result xrpRPCError) string {
	if result.Error != "" {
		return result.Error
	}
	if result.Status != "" {
		return result.Status
	}
	return "unknown"
}
