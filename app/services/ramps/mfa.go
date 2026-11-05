package ramps

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	enginemfa "github.com/macrowallets/waas/packages/rampengine/app/services/mfa"
)

// OperatorMFA checks the authenticated Macro operator, independently of the
// engine contact representing the person being verified. Integrator API
// requests use their explicit API-token scopes and existing wallet signing.
type OperatorMFA struct {
	Actor    *models.User
	Verifier *authsvc.SecondFactorVerifier
}

func (m OperatorMFA) Enrolled(identity.Subject) (bool, error) {
	return m.Actor != nil && m.Actor.TotpEnabled, nil
}
func (m OperatorMFA) ValidateStepUpCode(_ context.Context, _ identity.Subject, code, kind string) error {
	if m.Actor == nil || !m.Actor.TotpEnabled {
		return nil
	}
	totp, recovery := code, ""
	if kind == "backup" || kind == "recovery" {
		totp, recovery = "", code
	}
	if err := m.Verifier.Verify(m.Actor, totp, recovery); err != nil {
		if errors.Is(err, authsvc.ErrInvalidSecondFactor) {
			return enginemfa.ErrInvalidCode
		}
		return err
	}
	return nil
}
