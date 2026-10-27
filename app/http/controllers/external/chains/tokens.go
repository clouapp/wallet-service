package chains

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// Tokens lists only active registered token metadata for an active chain in the
// authenticated account's environment. It never fetches RPC or key material.
func (c *ChainController) Tokens(ctx http.Context) http.Response {
	environment, present := requestctx.AccountEnvironment(ctx)
	if !present || (environment != models.EnvironmentProd && environment != models.EnvironmentTest) {
		return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "account environment required")
	}
	chainID := ctx.Request().Route("chainId")
	chain, err := c.chains.FindForEnvironment(ctx.Context(), chainID, environment)
	if errors.Is(err, chainsvc.ErrChainNotInEnvironment) || errors.Is(err, models.ErrRepositoryNotFound) || (err == nil && (chain == nil || chain.Status != models.StatusActive)) {
		return tokenChainNotFound(ctx)
	}
	if err != nil {
		return mapError(ctx, err, "fetch chain tokens")
	}
	tokens, err := c.chains.TokensFor(ctx.Context(), chainID, environment)
	if errors.Is(err, chainsvc.ErrChainNotInEnvironment) || errors.Is(err, chainsvc.ErrChainNotFound) {
		return tokenChainNotFound(ctx)
	}
	if err != nil {
		return mapError(ctx, err, "fetch chain tokens")
	}
	return ctx.Response().Success().Json(http.Json{"data": chainresource.ActiveTokensFor(chainID, tokens)})
}
func tokenChainNotFound(ctx http.Context) http.Response {
	return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "chain not found")
}
