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
