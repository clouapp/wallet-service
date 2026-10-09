package features

import (
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	featuressvc "github.com/macrowallets/waas/app/services/features"
)

// ScopeRequest is the {scope}/{id} pair of a scoped flag write. Only the
// account scope exists, and its id is an account UUID; a write to anything
// else answers before the body is read.
type ScopeRequest struct {
	Scope string
	ID    string
}

// Decode reads the pair from the route. It returns the service's refusal for a
// scope that is not the account scope or an id that is not an account UUID.
func (r *ScopeRequest) Decode(ctx http.Context) error {
	r.Scope = strings.TrimSpace(ctx.Request().Route("scope"))
	r.ID = strings.TrimSpace(ctx.Request().Route("id"))
	if r.Scope != featuressvc.ScopeAccount {
		return featuressvc.ErrScopeNotFound
	}
	if id, err := uuid.Parse(r.ID); err != nil || id == uuid.Nil {
		return featuressvc.ErrInvalidAccountID
	}
	return nil
}
