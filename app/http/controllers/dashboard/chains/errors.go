package chains

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// mapError answers a chain service failure. A chain of the other network kind
// is 403 and a missing chain 404. A catalogue outage is the internal error
// without its cause. Any other failure is 500 "failed to <action>", with the
// cause logged and kept out of the body.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, chainsvc.ErrChainNotInEnvironment):
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, chainsvc.ErrChainNotInEnvironment.Error())
	case errors.Is(err, chainsvc.ErrChainNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "chain not found")
	case errors.Is(err, chainsvc.ErrChainLookup):
		return responses.InternalError(ctx, err)
	}
	slog.Error("chains: "+action, "error", err)
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to "+action)
}
