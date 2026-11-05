package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// FinalizeOfframpRequest validates POST /api/offramps/{id}/finalize: the
// transaction the wallet signed, base64 and exactly as signed, and the
// revision of the funding action it signed. The 4096-character bound is
// MaxUnsignedTransactionBytes as base64: the signed transaction is the
// unsigned one with its signature filled in. The {id} is the order's opaque
// external id from the path (which wins over a body value), a uuid like every
// order route's.
type FinalizeOfframpRequest struct {
	requests.Open
	ID                string `form:"id" json:"id"`
	SignedTransaction string `form:"signed_transaction" json:"signed_transaction"`
	ActionRevision    int    `form:"action_revision" json:"action_revision"`
} //	@name	ClientFinalizeOfframpRequest

func (r *FinalizeOfframpRequest) Rules(contractshttp.Context) map[string]string {
	return map[string]string{
		"id":                 "required|uuid",
		"signed_transaction": "required|max_len:4096",
		"action_revision":    "required|numeric|min:1",
	}
}
