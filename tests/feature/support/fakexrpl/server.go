// Package fakexrpl is an in-memory rippled JSON-RPC server for tests. It answers
// server_info, server_state, fee, account_info and tx from what the test sets, and
// fails the test on submit, so nothing is ever broadcast.
package fakexrpl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Account is an AccountRoot on the validated ledger. Balance is drops.
type Account struct {
	Balance    string
	Sequence   uint64
	OwnerCount uint64
	Flags      uint64
}

// RequireDestTag is lsfRequireDestTag.
const RequireDestTag = 0x00020000

// Tx is what the tx method reports. Result is meta.TransactionResult; Fee is drops.
// APIv2 puts Fee under tx_json, as rippled does for api_version 2.
type Tx struct {
	Validated   bool
	LedgerIndex uint64
	Result      string
	Fee         string
	APIv2       bool
}

// Server is the fake rippled. Set its fields before the first request.
type Server struct {
	URL string
	// NetworkID is server_info network_id (1 is the altnet).
	NetworkID uint64
	// ReserveBaseXRP and ReserveIncXRP are server_info reserve_base_xrp and
	// reserve_inc_xrp as JSON; "" omits the field.
	ReserveBaseXRP string
	ReserveIncXRP  string
	// StateReserveBase and StateReserveInc are server_state reserves in drops; with
	// StateReserveBase "" server_state answers an error.
	StateReserveBase string
	StateReserveInc  string
	OpenLedgerFee    string
	LedgerIndex      uint64

	t          testing.TB
	mu         sync.Mutex
	accounts   map[string]Account
	txs        map[string]Tx
	calls      []string
	ledgerUsed map[string]string
}

// New starts a fake altnet server: network id 1, 1 XRP base reserve, 0.2 XRP owner
// reserve, a 12-drop open-ledger fee, and validated ledger 100.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		NetworkID:      1,
		ReserveBaseXRP: "1",
		ReserveIncXRP:  "0.2",
		OpenLedgerFee:  "12",
		LedgerIndex:    100,
		t:              t,
		accounts:       map[string]Account{},
		txs:            map[string]Tx{},
		ledgerUsed:     map[string]string{},
	}
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	s.URL = server.URL
	return s
}

// Fund puts an account on the ledger.
func (s *Server) Fund(address string, account Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[address] = account
}

// SetTx makes the tx method answer for hash.
func (s *Server) SetTx(hash string, tx Tx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.txs[strings.ToUpper(hash)] = tx
}

// LedgerIndexFor is the ledger_index of the last account_info for address.
func (s *Server) LedgerIndexFor(address string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledgerUsed[address]
}

// Calls lists the RPC methods served, in order.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string           `json:"method"`
		Params []map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.t.Errorf("fakexrpl: request is not json: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	param := func(key string) string {
		if len(req.Params) == 0 {
			return ""
		}
		value, _ := req.Params[0][key].(string)
		return value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req.Method)
	var result string
	switch req.Method {
	case "server_info":
		var fields []string
		if s.ReserveBaseXRP != "" {
			fields = append(fields, `"reserve_base_xrp":`+s.ReserveBaseXRP)
		}
		if s.ReserveIncXRP != "" {
			fields = append(fields, `"reserve_inc_xrp":`+s.ReserveIncXRP)
		}
		fields = append(fields, fmt.Sprintf(`"seq":%d`, s.LedgerIndex))
		result = fmt.Sprintf(`{"status":"success","info":{"network_id":%d,"validated_ledger":{%s}}}`, s.NetworkID, strings.Join(fields, ","))
	case "server_state":
		if s.StateReserveBase == "" {
			result = `{"status":"error","error":"noNetwork"}`
			break
		}
		result = fmt.Sprintf(`{"status":"success","state":{"validated_ledger":{"reserve_base":%s,"reserve_inc":%s,"seq":%d}}}`, s.StateReserveBase, s.StateReserveInc, s.LedgerIndex)
	case "fee":
		result = fmt.Sprintf(`{"status":"success","drops":{"open_ledger_fee":%q,"minimum_fee":"10"}}`, s.OpenLedgerFee)
	case "ledger":
		result = fmt.Sprintf(`{"status":"success","ledger":{"ledger_index":"%d"}}`, s.LedgerIndex)
	case "account_info":
		s.ledgerUsed[param("account")] = param("ledger_index")
		account, ok := s.accounts[param("account")]
		if !ok {
			result = `{"status":"error","error":"actNotFound"}`
			break
		}
		result = fmt.Sprintf(`{"status":"success","account_data":{"Balance":%q,"Sequence":%d,"OwnerCount":%d,"Flags":%d}}`,
			account.Balance, account.Sequence, account.OwnerCount, account.Flags)
	case "tx":
		hash := strings.ToUpper(param("transaction"))
		tx, ok := s.txs[hash]
		if !ok {
			result = `{"status":"error","error":"txnNotFound"}`
			break
		}
		fee := fmt.Sprintf(`"Fee":%q,`, tx.Fee)
		txJSON := ""
		if tx.APIv2 {
			fee, txJSON = "", fmt.Sprintf(`"tx_json":{"Fee":%q},`, tx.Fee)
		}
		result = fmt.Sprintf(`{"status":"success","hash":%q,%s%s"validated":%t,"ledger_index":%d,"meta":{"TransactionResult":%q}}`,
			hash, fee, txJSON, tx.Validated, tx.LedgerIndex, tx.Result)
	case "submit":
		s.t.Errorf("fakexrpl: submit called; tests must never broadcast")
		result = `{"status":"error","error":"notAllowed"}`
	default:
		s.t.Errorf("fakexrpl: unexpected method %s", req.Method)
		result = `{"status":"error","error":"unknownCmd"}`
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"result":%s}`, result)
}
