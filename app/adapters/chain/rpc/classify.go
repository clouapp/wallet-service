package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/macrowallets/waas/app/services/chain"
)

// outcome is what one answer of one endpoint means for failover.
type outcome int

const (
	// outcomeAnswer is an answer about the request (a result, invalid params, a
	// reverted call, nonce too low, insufficient funds, ...). Another endpoint would
	// answer the same, so it is returned as it is and the endpoint counts as healthy.
	outcomeAnswer outcome = iota
	// outcomeUnsupported is an endpoint that does not serve the method. The call
	// moves on; the endpoint stays healthy for every other method.
	outcomeUnsupported
	// outcomeUnavailable is an endpoint that could not answer: transport error,
	// timeout, HTTP 5xx, an unreadable body, or a JSON-RPC error that names the node
	// (internal error, header not found, node behind). The call moves on and the
	// endpoint counts a failure.
	outcomeUnavailable
	// outcomeRateLimited is an endpoint that refused the call for capacity (HTTP 429
	// or 403, JSON-RPC 429 or -32005, a rate-limit message). The request was not
	// served: the call moves on and the endpoint counts a failure.
	outcomeRateLimited
)

// JSON-RPC codes the classification names. EIP-1474 and the Solana RPC share the
// standard range; -32004 is "method not supported" (EIP-1474) and "block not
// available for slot" (Solana), both answers this node cannot give.
const (
	solanaBlockCleanedUp        = -32001
	solanaSlotSkipped           = -32007
	solanaSlotMissingLongTerm   = -32009
	solanaHistoryNotAvailable   = -32011
	jsonRPCExecutionReverted    = 3
	jsonRPCServerError          = -32000
	jsonRPCInvalidParams        = -32602
	jsonRPCMethodNotFound       = -32601
	jsonRPCInternalError        = -32603
	jsonRPCMethodNotSupported   = -32004
	jsonRPCLimitExceeded        = -32005
	solanaBlockStatusNotYet     = -32014
	solanaMinContextSlotNotHit  = -32016
	httpStatusRequestTimeout    = 408
	httpStatusFirstServerFailed = 500
)

var (
	unsupportedPhrases = []string{
		"method not found", "method not supported", "unsupported method", "method not allowed",
		"method is not available", "method disabled", "is not whitelisted", "not whitelisted",
	}
	rateLimitPhrases = []string{
		"rate limit", "rate-limit", "ratelimit", "too many requests", "request limit", "requests limit",
		"daily limit", "quota", "capacity limit", "throttl",
	}
	unavailablePhrases = []string{
		"internal error", "header not found", "missing trie node", "unknown block", "upstream",
		"backend", "temporarily unavailable", "service unavailable", "no healthy", "node is behind",
		"node is unhealthy", "timeout", "timed out",
	}
	executionRevertedPhrase = "execution reverted"
	skippedPhrase           = "skipped"
	alreadyKnownPhrases     = []string{
		"already known", "known transaction", "alreadyknown", "already imported", "already exists",
		"already in mempool", "already been processed", "alreadyprocessed",
	}
)

// classifyRPCError decides what a JSON-RPC error of a 2xx answer means for failover.
func classifyRPCError(rpcErr *rpcError) outcome {
	return classifyRPCErrorOf(rpcErr, false)
}

// classifyRPCErrorOf decides what a JSON-RPC error means for failover. A contract
// revert (code 3, "execution reverted…") is always the contract's answer, whatever
// its reason string says. Then the codes above decide; the phrases are read only
// for the generic server errors (-32000, -32603) and for an error that came with a
// non-2xx status, so a provider-specific code with a word like "timeout" in it is
// not taken for the node's state. Every other error is an answer about the request.
func classifyRPCErrorOf(rpcErr *rpcError, httpFailure bool) outcome {
	if rpcErr == nil {
		return outcomeAnswer
	}
	message := strings.ToLower(rpcErr.Message)
	if rpcErr.Code == jsonRPCExecutionReverted || strings.Contains(message, executionRevertedPhrase) {
		return outcomeAnswer
	}
	switch rpcErr.Code {
	case jsonRPCRateLimitedCode, jsonRPCLimitExceeded:
		return outcomeRateLimited
	case jsonRPCMethodNotFound, jsonRPCMethodNotSupported, solanaBlockCleanedUp, solanaHistoryNotAvailable:
		// -32001 (block cleaned up) and -32011 (no transaction history) are a node
		// with a short history: another one may serve the call.
		return outcomeUnsupported
	case solanaBlockStatusNotYet, solanaMinContextSlotNotHit:
		return outcomeUnavailable
	case solanaSlotSkipped, solanaSlotMissingLongTerm:
		// A skipped slot says "skipped"; QuickNode answers its request limit with
		// the same code, which is not an answer about the slot.
		if strings.Contains(message, skippedPhrase) {
			return outcomeAnswer
		}
		if hasPhrase(message, rateLimitPhrases) {
			return outcomeRateLimited
		}
		return outcomeUnavailable
	}
	if httpFailure || rpcErr.Code == jsonRPCServerError || rpcErr.Code == jsonRPCInternalError {
		switch {
		case hasPhrase(message, rateLimitPhrases):
			return outcomeRateLimited
		case hasPhrase(message, unsupportedPhrases):
			return outcomeUnsupported
		case hasPhrase(message, unavailablePhrases):
			return outcomeUnavailable
		}
	}
	if rpcErr.Code == jsonRPCInternalError {
		return outcomeUnavailable
	}
	return outcomeAnswer
}

// classifyHTTPFailure decides what a non-2xx answer means. 5xx and 408 are an
// endpoint that could not answer. Any other status carrying a JSON-RPC error is
// classified by that error; without one, the endpoint did not serve the call.
func classifyHTTPFailure(status int, body []byte) outcome {
	if status >= httpStatusFirstServerFailed || status == httpStatusRequestTimeout {
		return outcomeUnavailable
	}
	var resp rpcResponse
	if json.Unmarshal(body, &resp) == nil && resp.Error != nil {
		return classifyRPCErrorOf(resp.Error, true)
	}
	return outcomeUnavailable
}

// shapeKey names the shape of a call that a node without a feature refuses as a
// whole, not because this one request is wrong: eth_getBlockReceipts by block
// number, and eth_getLogs by blockHash (EIP-234). Only those refusals (-32602) are
// remembered per endpoint (failover.go). Any other call, a broadcast above all, has
// no shape: a misrouted "method not found" is never remembered.
func shapeKey(method string, params []interface{}) string {
	switch method {
	case "eth_getBlockReceipts":
		return method
	case "eth_getLogs":
		if len(params) == 1 {
			if filter, ok := params[0].(map[string]interface{}); ok {
				if _, byHash := filter["blockHash"]; byHash {
					return "eth_getLogs(blockHash)"
				}
			}
		}
	}
	return ""
}

// shapeRefused reports the answer of a node that does not take a call's shape.
func shapeRefused(err error) bool {
	var rpcErr *rpcError
	return errors.As(err, &rpcErr) && rpcErr != nil && rpcErr.Code == jsonRPCInvalidParams
}

// skippedSlotAnswer reports a Solana getBlock answer that the slot was skipped (or
// is missing from the node's ledger, which a node also reports as "skipped").
func skippedSlotAnswer(err error) bool {
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return false
	}
	return (rpcErr.Code == solanaSlotSkipped || rpcErr.Code == solanaSlotMissingLongTerm) &&
		strings.Contains(strings.ToLower(rpcErr.Message), skippedPhrase)
}

// alreadyKnown reports a broadcast refusal that means the network already holds
// these exact bytes ("already known", Solana "already been processed").
func alreadyKnown(err error) bool {
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return false
	}
	return hasPhrase(strings.ToLower(rpcErr.Message), alreadyKnownPhrases)
}

// notAccepted reports a failed broadcast attempt that certainly did not hand the
// transaction to the endpoint: the connection was never made, or the endpoint
// refused the call before reading it (rate limit, method not served). A timeout,
// a reset, a 5xx or any answer may mean it was accepted.
func notAccepted(kind outcome, err error) bool {
	switch kind {
	case outcomeRateLimited, outcomeUnsupported:
		return true
	case outcomeUnavailable:
		return chain.RequestNotSent(err)
	default:
		return false
	}
}

// failureLabel is a short description of a failure for logs and health records.
// It carries no provider sentence, URL or key.
func failureLabel(kind outcome, err error) string {
	var rpcErr *rpcError
	var failure *chain.Failure
	switch {
	case errors.As(err, &rpcErr) && rpcErr != nil:
		return fmt.Sprintf("json-rpc error %d", rpcErr.Code)
	case kind == outcomeRateLimited:
		return "rate limited"
	case errors.As(err, &failure) && failure != nil && failure.Status != 0:
		return fmt.Sprintf("http %d", failure.Status)
	case errors.Is(err, chain.ErrWrongNetwork):
		return "wrong network"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case chain.RequestNotSent(err):
		return "connection failed"
	case timedOut(err):
		return "timeout"
	case kind == outcomeUnavailable:
		return "unavailable"
	default:
		return "error"
	}
}

func timedOut(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func hasPhrase(message string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}
