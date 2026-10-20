package xrp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
)

// Engine results of a submit (the preliminary result of the server that took it).
const (
	xrpEngineQueued    = "terQUEUED"
	xrpEngineAlready   = "tefALREADY"
	xrpEnginePastSeq   = "tefPAST_SEQ"
	xrpEngineMaxLedger = "tefMAX_LEDGER"
)

// xrpSubmitRefusedError is a payment the server read and refused, which no ledger
// will ever apply: malformed (tem), failed before any fee was claimed (tef), or a
// request error (invalidTransaction, a fee above the limit). It was not sent and is
// not sent again.
type xrpSubmitRefusedError struct {
	code string
}

func (e *xrpSubmitRefusedError) Error() string { return "xrp submit: " + e.code }

// judgeXRPSubmit says what a submit result means for failover. Applied or queued
// (tes, terQUEUED), already applied (tefALREADY), or applied with a claimed fee and
// a failure the validated ledger will record (tec): the bytes were sent. A local
// error (tel) is a server that neither applied nor relayed them (chain.NotSent):
// the next endpoint is asked. tefPAST_SEQ and tefMAX_LEDGER may follow an earlier
// application of these bytes and a ter result is held for a later ledger: the
// outcome is unknown. Any other refusal is a known failure.
func judgeXRPSubmit(result json.RawMessage, txHash string) (rpc.Outcome, error) {
	var answer struct {
		xrpRPCError
		EngineResult string `json:"engine_result"`
		TxJSON       struct {
			Hash string `json:"hash"`
		} `json:"tx_json"`
	}
	if err := json.Unmarshal(result, &answer); err != nil {
		return rpc.OutcomeUnavailable, fmt.Errorf("xrp submit: result is not the expected shape: %w", err)
	}
	if answer.Status != "success" {
		return rpc.OutcomeAnswer, &xrpSubmitRefusedError{code: xrpErrorCode(answer.xrpRPCError)}
	}
	if answer.TxJSON.Hash != "" && !strings.EqualFold(answer.TxJSON.Hash, txHash) {
		return rpc.OutcomeAnswer, chain.UnknownOutcome(fmt.Errorf("xrp submit returned a different hash"))
	}
	engine := answer.EngineResult
	switch {
	case strings.HasPrefix(engine, "tes"), engine == xrpEngineQueued, engine == xrpEngineAlready, strings.HasPrefix(engine, "tec"):
		return rpc.OutcomeAnswer, nil
	case strings.HasPrefix(engine, "tel"):
		return rpc.OutcomeUnavailable, chain.NotSent(&xrpSubmitRefusedError{code: engine})
	case engine == xrpEnginePastSeq, engine == xrpEngineMaxLedger, strings.HasPrefix(engine, "ter"), engine == "":
		return rpc.OutcomeAnswer, chain.UnknownOutcome(fmt.Errorf("xrp submit: %q", engine))
	default:
		return rpc.OutcomeAnswer, &xrpSubmitRefusedError{code: engine}
	}
}

// classifyXRPSubmit keeps a submit failure that proves the bytes were not taken (a
// refusal, a request that never left or that no server served) or that is already
// an unknown outcome, and marks every other one an unknown outcome: a timeout, a
// dropped connection, an HTTP 5xx or an answer that cannot be read may hide an
// acceptance, so the payment is reconciled by its hash, never signed again.
func classifyXRPSubmit(err error) error {
	var refused *xrpSubmitRefusedError
	switch {
	case err == nil:
		return nil
	case chain.RequestNotSent(err), errors.Is(err, chain.ErrRateLimited), errors.Is(err, chain.ErrUnknownOutcome):
		return err
	case errors.As(err, &refused):
		return err
	default:
		return chain.UnknownOutcome(err)
	}
}
