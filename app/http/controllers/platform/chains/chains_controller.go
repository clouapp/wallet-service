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
// a chain row. S1.4.4 names chains.update. That name is not in the code
// catalog, so a platform_admins row is the gate, the same gate the other
// /v1/platform routes use. An unknown chain is 404 before that check.
type ChainsController struct {
	thresholds *chainsvc.Thresholds
	rpc        *chainsvc.RPC
}

// NewChainsController wires the platform chain handlers.
func NewChainsController(thresholds *chainsvc.Thresholds, rpc *chainsvc.RPC) *ChainsController {
	if thresholds == nil {
		panic("platform chains controller: thresholds service is required")
	}
	if rpc == nil {
		panic("platform chains controller: rpc service is required")
	}
	return &ChainsController{thresholds: thresholds, rpc: rpc}
}

// Update godoc
// @Summary      Update chain sweep thresholds
// @Description  Writes gas_readiness_threshold_raw, dust_threshold_native_raw, and dust_threshold_usd. An omitted field is left unchanged. An unknown chain is 404 before the platform-admin check. A negative amount is 422 and is not stored. The RPC URL is not accepted and is not returned.
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
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.ChainIDRequest
	path.Load(ctx)
	if path.ChainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}
	document, err := requests.ChainThresholdDocument(ctx)
	if err != nil {
		return mapChainThresholdBodyError(ctx, err)
	}
	view, err := ctrl.thresholds.Update(ctx.Context(), actorID, path.ChainID, document)
	if errResp := mapChainThresholdError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

// UpdateRPC godoc
// @Summary      Replace a chain RPC endpoint
// @Description  Seals a new rpc_url. The answer is rpcUrlSet. The URL is not returned. An unknown chain is 404 before the platform-admin check. An empty URL is 422 and is not stored.
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
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthorized"})
	}
	var path requests.ChainIDRequest
	path.Load(ctx)
	if path.ChainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "chainId is required"})
	}
	document, err := requests.ChainRPCDocument(ctx)
	if err != nil {
		return mapChainRPCBodyError(ctx, err)
	}
	view, err := ctrl.rpc.Update(ctx.Context(), actorID, path.ChainID, document)
	if errResp := mapChainRPCError(ctx, err); errResp != nil {
		return errResp
	}
	return responses.Send(ctx, http.StatusOK, view)
}

func mapChainRPCBodyError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrChainRPCBodyTooLarge) {
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	}
	return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
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
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	case errors.Is(err, chainsvc.ErrPlatformForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}

func mapChainThresholdBodyError(ctx http.Context, err error) http.Response {
	if errors.Is(err, requests.ErrChainThresholdBodyTooLarge) {
		return responses.Send(ctx, http.StatusRequestEntityTooLarge, http.Json{"error": "request body is too large"})
	}
	return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
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
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "chain not found"})
	case errors.Is(err, chainsvc.ErrPlatformForbidden):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	default:
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal_error"})
	}
}
