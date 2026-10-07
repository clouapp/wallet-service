package xrp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
)

const xrpAccountNotFound = "actNotFound"

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

func (a *Live) accountSequence(ctx context.Context, address string) (uint64, error) {
	var result struct {
		xrpRPCError
		AccountData struct {
			Sequence json.Number `json:"Sequence"`
		} `json:"account_data"`
	}
	err := a.call(ctx, "account_info", []any{map[string]string{
		"account":      address,
		"ledger_index": "validated",
	}}, &result)
	if err != nil {
		return 0, err
	}
	if result.Status != "success" {
		return 0, fmt.Errorf("xrp account_info sequence: %s", xrpErrorCode(result.xrpRPCError))
	}
	sequence, err := nonNegativeJSON(result.AccountData.Sequence)
	if err != nil {
		return 0, fmt.Errorf("xrp account_info sequence: %w", err)
	}
	return sequence, nil
}

func (a *Live) accountExists(ctx context.Context, address string) (bool, error) {
	var result struct {
		xrpRPCError
	}
	err := a.call(ctx, "account_info", []any{map[string]string{
		"account":      address,
		"ledger_index": "validated",
	}}, &result)
	if err != nil {
		return false, err
	}
	if result.Status == "error" && result.Error == xrpAccountNotFound {
		return false, nil
	}
	if result.Status != "success" {
		return false, fmt.Errorf("xrp account_info: %s", xrpErrorCode(result.xrpRPCError))
	}
	return true, nil
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
	NetworkID    uint64
	ReserveDrops *big.Int
	LedgerIndex  uint64
}

func (a *Live) ledgerFacts(ctx context.Context) (xrpLedgerFacts, error) {
	var result struct {
		xrpRPCError
		Info struct {
			NetworkID       json.Number `json:"network_id"`
			ValidatedLedger struct {
				ReserveBase json.RawMessage `json:"reserve_base_xrp"`
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
	reserve, err := xrpReserveDrops(result.Info.ValidatedLedger.ReserveBase)
	if err != nil {
		return xrpLedgerFacts{}, err
	}
	return xrpLedgerFacts{NetworkID: networkID, ReserveDrops: reserve, LedgerIndex: ledger}, nil
}

func (a *Live) transactionLedger(ctx context.Context, txHash string) (uint64, error) {
	if txHash == "" {
		return 0, fmt.Errorf("xrp tx hash is required")
	}
	var result struct {
		xrpRPCError
		Validated   bool        `json:"validated"`
		LedgerIndex json.Number `json:"ledger_index"`
	}
	err := a.call(ctx, "tx", []any{map[string]any{"transaction": txHash, "binary": false}}, &result)
	if err != nil {
		return 0, err
	}
	if result.Status == "error" && result.Error == "txnNotFound" {
		return 0, nil
	}
	if result.Status != "success" {
		return 0, fmt.Errorf("xrp tx: %s", xrpErrorCode(result.xrpRPCError))
	}
	if !result.Validated {
		return 0, nil
	}
	ledger, err := nonNegativeJSON(result.LedgerIndex)
	if err != nil {
		return 0, fmt.Errorf("xrp tx ledger: %w", err)
	}
	return ledger, nil
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
