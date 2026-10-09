package users

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
)

// DisableTotpRequest is the proof required to turn 2FA off: the current
// TOTP code, or one unused recovery code.
type DisableTotpRequest struct {
	Code         string `form:"code" json:"code"`
	RecoveryCode string `form:"recovery_code" json:"recovery_code"`
}

func (r *DisableTotpRequest) Authorize(ctx http.Context) error { return nil }

func (r *DisableTotpRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{}
}

// DisableTotpProof is the proof of DELETE /v1/users/me/totp, read from the
// body only when the TOTP service asks for it: the body of a user without 2FA
// is never read, as it never was.
type DisableTotpProof struct {
	ctx http.Context
}

// NewDisableTotpProof reads the proof from ctx's body on demand.
func NewDisableTotpProof(ctx http.Context) DisableTotpProof {
	return DisableTotpProof{ctx: ctx}
}

// Read runs DisableTotpRequest. A body that does not bind is a
// *requests.Refusal carrying the answer requests.Validate gave.
func (p DisableTotpProof) Read() (string, string, error) {
	var req DisableTotpRequest
	if response := requests.Validate(p.ctx, &req); response != nil {
		return "", "", &requests.Refusal{Response: response}
	}
	return req.Code, req.RecoveryCode, nil
}
