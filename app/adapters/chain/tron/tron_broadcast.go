package tron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
)

const tronBroadcastPath = "/wallet/broadcasthex"

// java-tron broadcast codes (Wallet.broadcastTransaction). NO_CONNECTION and
// SERVER_BUSY are checked before the node pushes the transaction: those refusals
// never hand the bytes to the network. NOT_ENOUGH_EFFECTIVE_CONNECTION is also what
// the node answers after the push when it reached no peer ("P2P broadcast failed."),
// and OTHER_ERROR can follow the push too. DUP_TRANSACTION_ERROR comes from the
// node's recent-id cache, which also holds the id of a push it refused.
const (
	tronDuplicateTransaction    = "DUP_TRANSACTION_ERROR"
	tronServerBusy              = "SERVER_BUSY"
	tronNoConnection            = "NO_CONNECTION"
	tronNotEnoughConnections    = "NOT_ENOUGH_EFFECTIVE_CONNECTION"
	tronOtherError              = "OTHER_ERROR"
	tronBroadcastRefusedPattern = "tron broadcast refused: %s"
	tronTransactionByIDPath     = "/wallet/gettransactionbyid"
)

// tronBroadcastAnswer is /wallet/broadcasthex's answer.
type tronBroadcastAnswer struct {
	tronCallResult
	TxID string `json:"txid"`
}

// tronBroadcastRefusedError is a node that read the transaction and refused it under
// its rules (bad signature, contract validation, bandwidth, expiry): any node would
// refuse the same bytes, so it was not sent and is not sent again.
type tronBroadcastRefusedError struct {
	answer tronCallResult
}

func (e *tronBroadcastRefusedError) Error() string {
	return fmt.Sprintf(tronBroadcastRefusedPattern, e.answer.describe())
}

// tronDuplicateError is a node that answered DUP_TRANSACTION_ERROR: it holds these
// bytes, or only remembers their id from a push it refused. BroadcastTransaction
// looks the id up before it believes either.
type tronDuplicateError struct {
	answer tronCallResult
}

func (e *tronDuplicateError) Error() string {
	return fmt.Sprintf(tronBroadcastRefusedPattern, e.answer.describe())
}

// judgeTronBroadcast says what a 2xx broadcast answer means for failover: accepted is
// the answer; a duplicate is the answer, checked by its id afterwards; a node
// without connections or too busy did not take the bytes (chain.NotSent), so the
// next endpoint is asked; a node that reached no peer or failed after the push may
// hold them (an unknown outcome, never sent elsewhere); any other refusal is the
// answer, a known failure; a body that does not decode may hide an acceptance.
func judgeTronBroadcast(raw []byte) (rpc.Outcome, error) {
	var answer tronBroadcastAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return rpc.OutcomeUnavailable, fmt.Errorf("parse tron broadcast answer: %w", err)
	}
	refused := &tronBroadcastRefusedError{answer: answer.tronCallResult}
	switch {
	case answer.Result:
		return rpc.OutcomeAnswer, nil
	case answer.Code == tronDuplicateTransaction:
		return rpc.OutcomeAnswer, &tronDuplicateError{answer: answer.tronCallResult}
	case answer.Code == tronServerBusy, answer.Code == tronNoConnection:
		return rpc.OutcomeUnavailable, chain.NotSent(refused)
	case answer.Code == tronNotEnoughConnections, answer.Code == tronOtherError:
		return rpc.OutcomeUnavailable, chain.UnknownOutcome(refused)
	default:
		return rpc.OutcomeAnswer, refused
	}
}

// knownByID reports whether any endpoint of the chain's network has the transaction
// txID (/wallet/gettransactionbyid answers it, in a block or not).
func (a *TronLive) knownByID(ctx context.Context, txID string) (bool, error) {
	body, err := json.Marshal(map[string]any{"value": txID})
	if err != nil {
		return false, err
	}
	return a.endpoints.Any(ctx, func(ctx context.Context, at rpc.Attempt) (bool, error) {
		raw, _, err := a.exchange(ctx, at, tronTransactionByIDPath, body)
		if err != nil {
			return false, err
		}
		var tx struct {
			TxID string `json:"txID"`
		}
		if err := json.Unmarshal(raw, &tx); err != nil {
			return false, fmt.Errorf("parse tron transaction %s: %w", txID, err)
		}
		return strings.EqualFold(tx.TxID, txID), nil
	})
}

// classifyTronBroadcast keeps a broadcast failure that proves the bytes were not
// taken (a refusal, a request that never left or that no endpoint served) and marks
// every other one an unknown outcome: a timeout, a dropped connection, an HTTP 5xx
// or an answer that cannot be read may hide an acceptance, so the transfer is
// reconciled by its hash, never signed again.
func classifyTronBroadcast(err error) error {
	var refused *tronBroadcastRefusedError
	var nodeErr *tronNodeError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, chain.ErrUnknownOutcome), chain.RequestNotSent(err), errors.Is(err, chain.ErrRateLimited):
		return err
	case errors.As(err, &refused), errors.As(err, &nodeErr):
		return err
	default:
		return chain.UnknownOutcome(err)
	}
}
