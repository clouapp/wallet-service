package xrp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/macrowallets/waas/app/services/xrpstream"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// streamIdleTimeout ends a session that hears nothing: the ledger stream sends a
	// message for every validated ledger, one every 3-5 s.
	streamIdleTimeout = 30 * time.Second
	// streamRequestTimeout bounds the wait for the answer to one request.
	streamRequestTimeout = 30 * time.Second
	// streamWriteTimeout bounds one write, below the local shutdown timeout
	// (SHUTDOWN_TIMEOUT_SECONDS, default 15 s).
	streamWriteTimeout = 5 * time.Second
	streamDialTimeout  = 15 * time.Second
	// streamSubscribeBatch is how many accounts one subscribe request names.
	streamSubscribeBatch = 500
	// streamMaxQueued is how many stream messages may wait while an answer is
	// awaited; more ends the session (the server streams faster than it answers).
	streamMaxQueued = 1000
	// mainnetNetworkID is the network_id of the XRP Ledger mainnet, the one a
	// server that names none serves.
	mainnetNetworkID       = 0
	streamTypeResponse     = "response"
	streamTypeLedgerClosed = "ledgerClosed"
	streamTypeTransaction  = "transaction"
	streamStatusSuccess    = "success"
)

// StreamConfig is one XRPL WebSocket endpoint (rippled or Clio) of a chain.
type StreamConfig struct {
	ChainIDStr   string
	NativeSymbol string
	// URL is the wss:// endpoint (ws:// only on the loopback). It may carry a key:
	// it is never logged and dial errors drop it.
	URL string
	// IsTestnet is the chain's network: every session checks the server's
	// network_id is the altnet's (1), or the mainnet's (0, also a server that names
	// none), since XRP addresses are the same on every network.
	IsTestnet bool
	// IdleTimeout ends a session that receives nothing for this long; 0 keeps 30 s.
	IdleTimeout time.Duration
}

// StreamDialer opens the WebSocket sessions of the real-time XRP deposit listener.
type StreamDialer struct {
	cfg    StreamConfig
	dialer *websocket.Dialer
}

var _ xrpstream.Dialer = (*StreamDialer)(nil)

// NewStreamDialer builds a dialer. It does not dial.
func NewStreamDialer(cfg StreamConfig) *StreamDialer {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = streamIdleTimeout
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = streamDialTimeout
	return &StreamDialer{cfg: cfg, dialer: &dialer}
}

// Dial opens a session and checks the server serves the chain's network
// (server_info network_id). An endpoint that is neither wss:// nor ws:// on the
// loopback is refused before any network call.
func (d *StreamDialer) Dial(ctx context.Context) (xrpstream.Session, error) {
	rawURL := strings.TrimSpace(d.cfg.URL)
	if err := checkStreamURL(rawURL); err != nil {
		return nil, fmt.Errorf("xrp stream %s: %w", d.cfg.ChainIDStr, err)
	}
	conn, response, err := d.dialer.DialContext(ctx, rawURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, httpclient.RedactURL(fmt.Errorf("xrp stream %s dial: %w", d.cfg.ChainIDStr, err), rawURL)
	}
	conn.SetReadLimit(xrpLedgerMaxResponseBytes)
	session := &streamSession{conn: conn, cfg: d.cfg}
	if err := session.checkNetwork(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return session, nil
}

// checkStreamURL accepts wss://, and ws:// only to a loopback host: a plain
// WebSocket elsewhere would carry the subscription, and any key in the URL, in the
// clear.
func checkStreamURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("the endpoint is not a WebSocket URL")
	}
	switch parsed.Scheme {
	case "wss":
		return nil
	case "ws":
		host := parsed.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
		return fmt.Errorf("a ws:// endpoint must be on the loopback; use wss://")
	}
	return fmt.Errorf("the endpoint must be a wss:// URL")
}

// checkNetwork refuses a server of another network than the chain's: the
// addresses are the same on every network, so a payment heard there would name a
// watched address.
func (s *streamSession) checkNetwork(ctx context.Context) error {
	var result struct {
		Info struct {
			NetworkID json.Number `json:"network_id"`
		} `json:"info"`
	}
	if err := s.request(ctx, map[string]any{"command": "server_info"}, &result); err != nil {
		return fmt.Errorf("xrp stream %s: network check: %w", s.cfg.ChainIDStr, err)
	}
	served, err := optionalNonNegativeJSON(result.Info.NetworkID)
	if err != nil {
		return fmt.Errorf("xrp stream %s: network check: network_id %w", s.cfg.ChainIDStr, err)
	}
	want := uint64(mainnetNetworkID)
	if s.cfg.IsTestnet {
		want = altnetNetworkID
	}
	if served != want {
		return fmt.Errorf("xrp stream %s: the endpoint serves network %d, the chain is on network %d", s.cfg.ChainIDStr, served, want)
	}
	return nil
}

// streamSession is one connection. Requests are answered in turn; a stream message
// read while an answer is awaited is queued for Next.
type streamSession struct {
	conn   *websocket.Conn
	cfg    StreamConfig
	nextID uint64
	queued [][]byte
}

// streamEnvelope is what every message is told apart by.
type streamEnvelope struct {
	Type   string          `json:"type"`
	ID     json.Number     `json:"id"`
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Result json.RawMessage `json:"result"`
}

// streamError is a request the server answered with an error code.
type streamError struct {
	command string
	code    string
}

func (e *streamError) Error() string { return fmt.Sprintf("xrp %s: %s", e.command, e.code) }

func (s *streamSession) SubscribeLedgers(ctx context.Context) (uint64, error) {
	var result struct {
		LedgerIndex json.Number `json:"ledger_index"`
	}
	if err := s.request(ctx, map[string]any{"command": "subscribe", "streams": []string{"ledger"}}, &result); err != nil {
		return 0, err
	}
	ledger, err := nonNegativeJSON(result.LedgerIndex)
	if err != nil {
		return 0, fmt.Errorf("xrp subscribe: validated ledger: %w", err)
	}
	return ledger, nil
}

func (s *streamSession) SubscribeAccounts(ctx context.Context, accounts []string) error {
	return s.accounts(ctx, "subscribe", accounts)
}

func (s *streamSession) UnsubscribeAccounts(ctx context.Context, accounts []string) error {
	return s.accounts(ctx, "unsubscribe", accounts)
}

// accounts sends command for accounts, streamSubscribeBatch at a time.
func (s *streamSession) accounts(ctx context.Context, command string, accounts []string) error {
	for start := 0; start < len(accounts); start += streamSubscribeBatch {
		batch := accounts[start:min(start+streamSubscribeBatch, len(accounts))]
		var result json.RawMessage
		if err := s.request(ctx, map[string]any{"command": command, "accounts": batch}, &result); err != nil {
			return err
		}
	}
	return nil
}

// Next returns the next stream message: a validated ledger (ledgerClosed), or a
// transaction with the deposit it is, if any. Other messages are an empty event.
func (s *streamSession) Next(ctx context.Context) (xrpstream.Event, error) {
	raw, err := s.nextMessage(ctx)
	if err != nil {
		return xrpstream.Event{}, err
	}
	var head struct {
		Type        string      `json:"type"`
		LedgerIndex json.Number `json:"ledger_index"`
		Validated   bool        `json:"validated"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return xrpstream.Event{}, fmt.Errorf("xrp stream: message is not json")
	}
	switch head.Type {
	case streamTypeLedgerClosed:
		ledger, err := nonNegativeJSON(head.LedgerIndex)
		if err != nil {
			return xrpstream.Event{}, fmt.Errorf("xrp stream: ledgerClosed index: %w", err)
		}
		return xrpstream.Event{Ledger: ledger}, nil
	case streamTypeTransaction:
		if !head.Validated {
			return xrpstream.Event{}, nil
		}
		event := xrpstream.Event{}
		event.Ledger, _ = nonNegativeJSON(head.LedgerIndex)
		if transfer, ok := s.deposit(raw); ok {
			event.Deposits = []types.DetectedTransfer{transfer}
		}
		return event, nil
	}
	return xrpstream.Event{}, nil
}

func (s *streamSession) Close() error { return s.conn.Close() }

// deposit reads one validated transaction (a stream message or an account_tx
// entry) as the ledger scan reads it.
func (s *streamSession) deposit(raw json.RawMessage) (types.DetectedTransfer, bool) {
	var tx xrpValidatedTx
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&tx); err != nil {
		slog.Warn("xrp stream: transaction is not the expected shape; the ledger scan reads it", "chain", s.cfg.ChainIDStr)
		return types.DetectedTransfer{}, false
	}
	transfer, ok, err := tx.deposit(s.cfg.NativeSymbol)
	if err != nil {
		slog.Warn("xrp stream: transaction not read; the ledger scan reads it", "chain", s.cfg.ChainIDStr, "error", err)
		return types.DetectedTransfer{}, false
	}
	return transfer, ok
}

// request sends command with the next id and waits for its answer, decoded into
// result. Stream messages read meanwhile are queued for Next.
func (s *streamSession) request(ctx context.Context, command map[string]any, result any) error {
	s.nextID++
	id := s.nextID
	command["id"] = id
	name, _ := command["command"].(string)
	if err := s.write(ctx, command); err != nil {
		return fmt.Errorf("xrp %s: send: %w", name, err)
	}
	deadline := time.Now().Add(streamRequestTimeout)
	for {
		raw, err := s.read(ctx, deadline)
		if err != nil {
			return fmt.Errorf("xrp %s: %w", name, err)
		}
		var envelope streamEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("xrp %s: message is not json", name)
		}
		if envelope.Type != streamTypeResponse {
			if len(s.queued) >= streamMaxQueued {
				return fmt.Errorf("xrp %s: more than %d stream messages while the answer was awaited", name, streamMaxQueued)
			}
			s.queued = append(s.queued, raw)
			continue
		}
		if envelope.ID.String() != fmt.Sprint(id) {
			continue
		}
		if envelope.Status != streamStatusSuccess {
			return &streamError{command: name, code: xrpErrorCode(xrpRPCError{Status: envelope.Status, Error: envelope.Error})}
		}
		decoder := json.NewDecoder(bytes.NewReader(envelope.Result))
		decoder.UseNumber()
		if err := decoder.Decode(result); err != nil {
			return fmt.Errorf("xrp %s: result is not the expected shape", name)
		}
		return nil
	}
}

// write sends message within streamWriteTimeout (or ctx's deadline, if sooner); ctx
// ending first ends the write.
func (s *streamSession) write(ctx context.Context, message any) error {
	deadline := time.Now().Add(streamWriteTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.conn.SetWriteDeadline(time.Now()) })
	defer stop()
	if err := s.conn.WriteJSON(message); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// nextMessage is the oldest queued stream message, or the next one read within
// the idle timeout.
func (s *streamSession) nextMessage(ctx context.Context) ([]byte, error) {
	if len(s.queued) > 0 {
		raw := s.queued[0]
		s.queued = s.queued[1:]
		return raw, nil
	}
	raw, err := s.read(ctx, time.Now().Add(s.cfg.IdleTimeout))
	if err != nil {
		return nil, fmt.Errorf("xrp stream: %w", err)
	}
	return raw, nil
}

// read reads one message by deadline; ctx ending first ends the read (and the
// connection, which a failed read leaves unusable).
func (s *streamSession) read(ctx context.Context, deadline time.Time) ([]byte, error) {
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.conn.SetReadDeadline(time.Now()) })
	defer stop()
	_, raw, err := s.conn.ReadMessage()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return raw, nil
}

// xrpTxFieldsWithHash is the transaction object of the API v1 stream
// ("transaction") and of account_tx ("tx"): the fields with the hash and, in
// account_tx, the ledger.
type xrpTxFieldsWithHash struct {
	xrpLedgerTxFields
	Hash        string      `json:"hash"`
	LedgerIndex json.Number `json:"ledger_index"`
}

// xrpValidatedTx is one transaction as the WebSocket stream and account_tx report
// it: API v1 puts the fields in "transaction" (stream) or "tx" (account_tx), API
// v2 in tx_json with the hash and the ledger next to it.
type xrpValidatedTx struct {
	Hash        string               `json:"hash"`
	LedgerIndex json.Number          `json:"ledger_index"`
	LedgerHash  string               `json:"ledger_hash"`
	Validated   bool                 `json:"validated"`
	Transaction *xrpTxFieldsWithHash `json:"transaction"`
	Tx          *xrpTxFieldsWithHash `json:"tx"`
	TxJSON      *xrpLedgerTxFields   `json:"tx_json"`
	Meta        *xrpLedgerTxMeta     `json:"meta"`
}

// deposit is the deposit tx is, read by the ledger scan's rule (xrpLedgerEntry); a
// transaction that is not validated is none.
func (tx xrpValidatedTx) deposit(asset string) (types.DetectedTransfer, bool, error) {
	if !tx.Validated {
		return types.DetectedTransfer{}, false, nil
	}
	inner := tx.Transaction
	if inner == nil {
		inner = tx.Tx
	}
	var fields xrpLedgerTxFields
	hash, ledger := tx.Hash, tx.LedgerIndex
	switch {
	case tx.TxJSON != nil:
		fields = *tx.TxJSON
	case inner != nil:
		fields = inner.xrpLedgerTxFields
		if hash == "" {
			hash = inner.Hash
		}
		if ledger == "" {
			ledger = inner.LedgerIndex
		}
	default:
		return types.DetectedTransfer{}, false, fmt.Errorf("transaction without fields")
	}
	if hash == "" {
		return types.DetectedTransfer{}, false, fmt.Errorf("transaction without a hash")
	}
	hash = strings.ToUpper(hash)
	if tx.Meta == nil || tx.Meta.TransactionResult == "" {
		return types.DetectedTransfer{}, false, fmt.Errorf("transaction %s has no metadata", hash)
	}
	ledgerIndex, err := nonNegativeJSON(ledger)
	if err != nil {
		return types.DetectedTransfer{}, false, fmt.Errorf("transaction %s ledger: %w", hash, err)
	}
	index, err := nonNegativeJSON(tx.Meta.TransactionIndex)
	if err != nil {
		return types.DetectedTransfer{}, false, fmt.Errorf("transaction %s index: %w", hash, err)
	}
	entry := xrpLedgerEntry{hash: hash, index: index, fields: fields, meta: *tx.Meta}
	return entry.deposit(ledgerIndex, tx.LedgerHash, time.Time{}, asset)
}
