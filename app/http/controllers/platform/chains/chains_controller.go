package chains

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainsController edits the sweep thresholds and the RPC endpoint stored on
// a chain row. S1.4.7 names chains.view and chains.update on the catalog
// entry these routes already use. There is no platform permission catalog,
// so a platform_admins row is the gate, the same gate the other /v1/platform
// routes use. The pair is not a second gate. An unknown chain is 404 before
// that check. The RPC URL is write-only and is never returned.
type ChainsController struct {
	thresholds *chainsvc.Thresholds
	rpc        *chainsvc.RPC
}

// ChainsControllerDeps is everything the platform chains controller needs.
type ChainsControllerDeps struct {
	Thresholds *chainsvc.Thresholds
	RPC        *chainsvc.RPC
}

// NewChainsController wires the platform chain handlers from ChainsControllerDeps.
func NewChainsController(deps ChainsControllerDeps) *ChainsController {
	if deps.Thresholds == nil {
		panic("platform chains controller: thresholds service is required")
	}
	if deps.RPC == nil {
		panic("platform chains controller: rpc service is required")
	}
	return &ChainsController{thresholds: deps.Thresholds, rpc: deps.RPC}
}

// Update godoc
// @Summary      Update chain sweep thresholds
// @Description  Writes gas_readiness_threshold_raw, dust_threshold_native_raw, and dust_threshold_usd. An omitted field is left unchanged. An unknown chain is 404 for a platform admin. A negative amount is 422 and is not stored. The RPC URL is not accepted and is not returned.
// @Tags         Platform Chains
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        chainId  path  string  true  "Chain id"
// @Success      200  {object}  chainsvc.ThresholdView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/chains/{chainId} [patch]
func (ctrl *ChainsController) Update(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	chainID := ctx.Request().Route("chainId")
	if chainID == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	document, err := requests.ChainThresholdDocument(ctx)
	if err != nil {
		return mapChainThresholdBodyError(ctx, err)
	}
	view, err := ctrl.thresholds.Update(ctx.Context(), actorID, chainID, document)
	if errResp := mapChainThresholdError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Success().Json(view)
}

// UpdateRPC godoc
// @Summary      Replace a chain RPC endpoint
// @Description  Seals a new rpc_url. The answer is rpcUrlSet. The URL is not returned. An unknown chain is 404 for a platform admin. An empty URL is 422 and is not stored.
// @Tags         Platform Chains
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        chainId  path  string  true  "Chain id"
// @Success      200  {object}  chainsvc.RPCView
// @Failure      401  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody
// @Router       /platform/chains/{chainId}/rpc [patch]
func (ctrl *ChainsController) UpdateRPC(ctx http.Context) http.Response {
	actorID := middleware.SessionUserID(ctx)
	if actorID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "unauthorized")
	}
	chainID := ctx.Request().Route("chainId")
	if chainID == "" {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "chainId is required")
	}
	document, err := requests.ChainRPCDocument(ctx)
	if err != nil {
		return mapChainRPCBodyError(ctx, err)
	}
	view, err := ctrl.rpc.Update(ctx.Context(), actorID, chainID, document)
	if errResp := mapChainRPCError(ctx, err); errResp != nil {
		return errResp
	}
	return ctx.Response().Success().Json(view)
}

func mapChainRPCBodyError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrChainRPCBodyTooLarge) {
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	}
	return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
}

func mapChainRPCError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	var invalid *chainsvc.ValidationError
	if errors.As(err, &invalid) {
		return responses.FieldsFailed(ctx, invalid.Fields)
	}
	switch {
	case errors.Is(err, chainsvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "chain not found")
	case errors.Is(err, chainsvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, chainsvc.ErrPlatformForbidden.Error())
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}

func mapChainThresholdBodyError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrChainThresholdBodyTooLarge) {
		return responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body is too large")
	}
	return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
}

func mapChainThresholdError(ctx http.Context, err error) http.Response {
	if err == nil {
		return nil
	}
	var invalid *chainsvc.ValidationError
	if errors.As(err, &invalid) {
		return responses.FieldsFailed(ctx, invalid.Fields)
	}
	switch {
	case errors.Is(err, chainsvc.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "chain not found")
	case errors.Is(err, chainsvc.ErrPlatformForbidden):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, chainsvc.ErrPlatformForbidden.Error())
	default:
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error")
	}
}
