package tron

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
)

const tronBroadcastPath = "/wallet/broadcasthex"

// java-tron broadcast codes (Wallet.broadcastTransaction). The node checks its
// connections and its pending queue before it reads the transaction: those
// refusals never hand the bytes to the network. A duplicate is a transaction the
// node already holds.
const (
	tronDuplicateTransaction    = "DUP_TRANSACTION_ERROR"
	tronServerBusy              = "SERVER_BUSY"
	tronNoConnection            = "NO_CONNECTION"
	tronNotEnoughConnections    = "NOT_ENOUGH_EFFECTIVE_CONNECTION"
	tronBroadcastRefusedPattern = "tron broadcast refused: %s"
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

// judgeTronBroadcast says what a 2xx broadcast answer means for failover: accepted
// or a duplicate is the answer; a node without connections or too busy did not take
// the bytes (chain.NotSent), so the next endpoint is asked; any other refusal is the
// answer, a known failure; a body that does not decode may hide an acceptance.
func judgeTronBroadcast(raw []byte) (rpc.Outcome, error) {
	var answer tronBroadcastAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return rpc.OutcomeUnavailable, fmt.Errorf("parse tron broadcast answer: %w", err)
	}
	switch {
	case answer.Result, answer.Code == tronDuplicateTransaction:
		return rpc.OutcomeAnswer, nil
	case answer.Code == tronServerBusy, answer.Code == tronNoConnection, answer.Code == tronNotEnoughConnections:
		return rpc.OutcomeUnavailable, chain.NotSent(&tronBroadcastRefusedError{answer: answer.tronCallResult})
	default:
		return rpc.OutcomeAnswer, &tronBroadcastRefusedError{answer: answer.tronCallResult}
	}
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
	case chain.RequestNotSent(err), errors.Is(err, chain.ErrRateLimited):
		return err
	case errors.As(err, &refused), errors.As(err, &nodeErr):
		return err
	default:
		return chain.UnknownOutcome(err)
	}
}
