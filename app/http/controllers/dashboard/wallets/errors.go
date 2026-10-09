package wallets

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/chainregistry"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletview"
)

// actionActivate names the activation in mapError: it has always answered an
// unexpected failure through the legacy JSON writer, not the envelope one.
const actionActivate = "activate wallet"

// mapError answers a wallet service failure. A caller who cannot be placed is
// 400 or 401, a wallet that is not the caller's 404, and a chain of the other
// network kind 403. A read that failed is 500 "failed to fetch <what>". The
// activation refusals keep the wallet service's own sentences. Keygen and share
// failures can carry key material, so for any other failure the log keeps the
// type and the body is internal error.
func mapError(ctx http.Context, err error, action string) http.Response {
	var fetch *walletview.FetchError
	switch {
	case errors.Is(err, walletview.ErrAccountRequired):
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, walletview.ErrAccountRequired.Error())
	case errors.Is(err, walletview.ErrViewerRequired):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, walletview.ErrViewerRequired.Error())
	case errors.Is(err, walletview.ErrWalletNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, walletview.ErrWalletNotFound.Error())
	case errors.As(err, &fetch):
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch "+fetch.What)
	case errors.Is(err, chainsvc.ErrChainNotInEnvironment):
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, chainsvc.ErrChainNotInEnvironment.Error())
	case errors.Is(err, chainregistry.ErrUnknownChain):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "unknown chain")
	case errors.Is(err, wallet.ErrWalletNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, wallet.ErrWalletNotFound.Error())
	case errors.Is(err, wallet.ErrWalletAlreadyActive):
		return responses.Error(ctx, http.StatusConflict, responses.CodeConflict, wallet.ErrWalletAlreadyActive.Error())
	case errors.Is(err, wallet.ErrInvalidActivationCode):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, wallet.ErrInvalidActivationCode.Error())
	}

	slog.Error(action+" failed", "error_type", fmt.Sprintf("%T", err))
	if action == actionActivate {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
	}
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}
