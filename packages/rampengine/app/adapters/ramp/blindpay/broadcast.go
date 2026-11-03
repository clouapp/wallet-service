package blindpay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

const (
	// broadcastConfirmWindow bounds how long a finalize waits for the node to
	// see the delegation. The payout create that follows reads the delegation
	// from the chain, so it must not be sent before it is there.
	broadcastConfirmWindow = 15 * time.Second
	broadcastPollInterval  = 600 * time.Millisecond
	rpcCallTimeout         = 8 * time.Second

	// The codes a refused delegation is stored under. They are read by an
	// operator, never by the client.
	refusalDelegationExpired  = "delegation_blockhash_expired"
	refusalDelegationRejected = "delegation_rejected"
	refusalDelegationFailed   = "delegation_failed"
	refusalDelegationUnread   = "delegation_unreadable"
)

// rpcRequest and rpcAnswer are the JSON-RPC envelope of a Solana node.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcAnswer struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// rpcError is a node refusing a call; it is the node's answer, so what it
// says is decided, unlike a transport failure.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("solana rpc error %d", e.Code) }

// signatureStatus is one entry of getSignatureStatuses.
type signatureStatus struct {
	ConfirmationStatus string          `json:"confirmationStatus"`
	Err                json.RawMessage `json:"err"`
}

func (s signatureStatus) confirmed() bool {
	return s.ConfirmationStatus == "confirmed" || s.ConfirmationStatus == "finalized"
}

func (s signatureStatus) failed() bool {
	trimmed := strings.TrimSpace(string(s.Err))
	return trimmed != "" && trimmed != "null"
}

// broadcastDelegation puts the signed delegation on the chain before the
// payout is created. BlindPay's POST payouts/solana reads the delegation from
// the chain and does not submit the transaction it is sent (S10, sandbox): a
// payout created before the delegation lands fails with
// solana_delegate_approval_missing.
//
// The answer is split by what is known. A transaction the node refuses, or
// that fails on chain, was never going to fund the payout: a
// *ramp.FinalizeRefusedError, so the order ends failed and the signed payload
// is not tried again. A node that does not answer, or does not confirm within
// the window, is left unmarked: the caller reads it as unknown and the
// recovery sends the same bytes again, which the node answers as already
// processed when the first send landed. No error carries the transaction.
func (a *Adapter) broadcastDelegation(ctx context.Context, client *Client, signedTransaction string) error {
	rpcURL := client.SolanaRPCURL()
	if rpcURL == "" {
		return nil
	}
	signature, err := firstSignature(signedTransaction)
	if err != nil {
		return &ramp.FinalizeRefusedError{Code: refusalDelegationUnread}
	}
	node := &solanaNode{url: rpcURL, http: &http.Client{Timeout: rpcCallTimeout}}

	if landed, err := node.landed(ctx, signature); err != nil || landed {
		return err
	}
	if err := node.send(ctx, signedTransaction); err != nil {
		return err
	}
	return node.awaitConfirmed(ctx, signature)
}

// firstSignature is the transaction id: its first signature, which the node
// answers by.
func firstSignature(signedTransaction string) (solana.Signature, error) {
	raw, err := base64.StdEncoding.DecodeString(signedTransaction)
	if err != nil {
		return solana.Signature{}, err
	}
	tx, err := solana.TransactionFromBytes(raw)
	if err != nil {
		return solana.Signature{}, err
	}
	if len(tx.Signatures) == 0 || tx.Signatures[0].IsZero() {
		return solana.Signature{}, errors.New("transaction is not signed")
	}
	return tx.Signatures[0], nil
}

type solanaNode struct {
	url  string
	http *http.Client
}

// call sends one JSON-RPC request. A node's refusal comes back as *rpcError;
// anything else (transport, status, an unreadable body) is a failure the node
// did not decide.
func (n *solanaNode) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("blindpay: solana %s: encode: %w", method, ramp.ErrRequestNotSent)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("blindpay: solana %s: %w", method, ramp.ErrRequestNotSent)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blindpay: solana %s: no answer: %w", method, ramp.ErrProviderFailed)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("blindpay: solana %s: read: %w", method, ramp.ErrProviderFailed)
	}
	var answer rpcAnswer
	if err := json.Unmarshal(raw, &answer); err != nil || (answer.Error == nil && answer.Result == nil) {
		return nil, fmt.Errorf("blindpay: solana %s: status %d, unreadable answer: %w", method, resp.StatusCode, ramp.ErrProviderFailed)
	}
	if answer.Error != nil {
		return nil, answer.Error
	}
	return answer.Result, nil
}

// status reads the signature's status; nil means the node has not seen it.
func (n *solanaNode) status(ctx context.Context, signature solana.Signature) (*signatureStatus, error) {
	raw, err := n.call(ctx, "getSignatureStatuses", []string{signature.String()}, map[string]any{"searchTransactionHistory": true})
	if err != nil {
		return nil, unmarkedRPC(err)
	}
	var out struct {
		Value []*signatureStatus `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Value) != 1 {
		return nil, fmt.Errorf("blindpay: solana status: unreadable answer: %w", ramp.ErrProviderFailed)
	}
	return out.Value[0], nil
}

// landed reports whether the delegation is already confirmed, so a resend of
// the same finalize does not send it again. A delegation that failed on chain
// is a refusal.
func (n *solanaNode) landed(ctx context.Context, signature solana.Signature) (bool, error) {
	status, err := n.status(ctx, signature)
	if err != nil || status == nil {
		return false, err
	}
	if status.failed() {
		return false, &ramp.FinalizeRefusedError{Code: refusalDelegationFailed}
	}
	return status.confirmed(), nil
}

// send submits the transaction. The node simulates it first (preflight), so a
// stale blockhash or an owner who cannot pay the fee is answered here.
func (n *solanaNode) send(ctx context.Context, signedTransaction string) error {
	_, err := n.call(ctx, "sendTransaction", signedTransaction, map[string]any{
		"encoding": "base64", "preflightCommitment": "confirmed", "maxRetries": 3,
	})
	var refused *rpcError
	switch {
	case err == nil:
		return nil
	case !errors.As(err, &refused):
		return err
	case strings.Contains(refused.Message, "already been processed"):
		return nil
	case strings.Contains(refused.Message, "Blockhash not found"), strings.Contains(refused.Message, "block height exceeded"):
		return &ramp.FinalizeRefusedError{Code: refusalDelegationExpired}
	}
	return &ramp.FinalizeRefusedError{Code: refusalDelegationRejected}
}

// awaitConfirmed waits for the node to confirm the delegation. A transaction
// that fails on chain is a refusal; one not confirmed in the window is left
// unmarked, because it may still land.
func (n *solanaNode) awaitConfirmed(ctx context.Context, signature solana.Signature) error {
	deadline := time.Now().Add(broadcastConfirmWindow)
	for {
		landed, err := n.landed(ctx, signature)
		if err != nil || landed {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("blindpay: delegation not confirmed in time: %w", ramp.ErrProviderFailed)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("blindpay: delegation wait: %w", ramp.ErrProviderFailed)
		case <-time.After(broadcastPollInterval):
		}
	}
}

// unmarkedRPC leaves a node's refusal of a read as a failed call, never a
// refusal of the payout: only a refused send decides one.
func unmarkedRPC(err error) error {
	var refused *rpcError
	if errors.As(err, &refused) {
		return fmt.Errorf("blindpay: solana read refused (%d): %w", refused.Code, ramp.ErrProviderFailed)
	}
	return err
}
