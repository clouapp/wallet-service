package controllers

import (
	"errors"
	"github.com/google/uuid"

	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	swaprequests "github.com/macrowallets/waas/app/http/requests/swaps"
	resources "github.com/macrowallets/waas/app/http/resources/swaps"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/mpcshare"
)

// SwapHandler shares quote and read behavior between the two authenticated surfaces.
type SwapHandler struct{ service *swap.Service }

// NewSwapHandler injects orchestration without persistence or provider clients.
func NewSwapHandler(service *swap.Service) *SwapHandler { return &SwapHandler{service: service} }

// Execute accepts or resumes exactly one order; the HTTP caller cannot set Resume.
func (c *SwapHandler) Execute(ctx http.Context) http.Response {
	var req swaprequests.ExecuteRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	defer mpcshare.DiscardPassphrase(&req.Passphrase)
	userID, _ := requestctx.UserID(ctx)
	token, _ := requestctx.APIToken(ctx)
	order, err := c.service.Execute(ctx.Context(), requestctx.MustAccount(ctx), requestctx.MustWallet(ctx), swap.ExecuteInput{QuoteID: uuid.MustParse(req.QuoteID), IdempotencyKey: uuid.MustParse(req.IdempotencyKey), UserID: userID, APIToken: token, TOTP: req.TOTP, Passphrase: req.Passphrase})
	if err != nil {
		return MapSwapError(ctx, err)
	}
	return ctx.Response().Status(http.StatusAccepted).Json(resources.NewOrder(order))
}

// Quote requests executable pricing between two account-owned custody wallets.
func (c *SwapHandler) Quote(ctx http.Context) http.Response {
	var req swaprequests.QuoteRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	destination, err := requests.RouteUUID(ctx, "destinationWalletId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid destination wallet id")
	}
	quote, err := c.service.CreateQuote(ctx.Context(), requestctx.MustAccount(ctx), requestctx.MustWallet(ctx), destination, swap.QuoteInput{FromAsset: req.FromAsset, ToAsset: req.ToAsset, Amount: req.Amount})
	if err != nil {
		return MapSwapError(ctx, err)
	}
	return ctx.Response().Status(http.StatusCreated).Json(resources.NewQuote(quote))
}

// Show returns an order scoped to the authenticated account and source wallet.
func (c *SwapHandler) Show(ctx http.Context) http.Response {
	id, err := requests.RouteUUID(ctx, "swapId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid swap id")
	}
	order, err := c.service.Refresh(ctx.Context(), requestctx.MustAccount(ctx), requestctx.MustWallet(ctx), id)
	if err != nil {
		return MapSwapError(ctx, err)
	}
	return ctx.Response().Success().Json(resources.NewOrder(order))
}

// Recover reads the original outcome after a timeout without reauthorizing a send.
func (c *SwapHandler) Recover(ctx http.Context) http.Response {
	id, err := requests.RouteUUID(ctx, "idempotencyKey")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid swap key")
	}
	order, err := c.service.Recover(ctx.Context(), requestctx.MustAccount(ctx), requestctx.MustWallet(ctx), id)
	if err != nil {
		return MapSwapError(ctx, err)
	}
	return ctx.Response().Success().Json(resources.NewOrder(order))
}

// MapSwapError translates domain refusals without exposing provider responses.
func MapSwapError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, swap.ErrWalletBlocked):
		return responses.Fail(ctx, http.StatusForbidden, "swap_wallet_blocked", "wallet cannot perform swaps")
	case errors.Is(err, swap.ErrInvalidPassphrase), errors.Is(err, swap.ErrInvalidTOTP):
		return responses.Fail(ctx, http.StatusUnauthorized, "invalid_swap_authorization", "invalid passphrase or authentication code")
	case errors.Is(err, swap.ErrNotFound):
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "swap not found")
	case errors.Is(err, swap.ErrDisabled):
		return responses.Fail(ctx, http.StatusConflict, "swaps_paused", "swaps are paused")
	case errors.Is(err, swap.ErrInvalidRequest):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, "invalid_swap_request", "invalid swap request")
	case errors.Is(err, swap.ErrNoRoute), errors.Is(err, swap.ErrUnsupported):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, "swap_route_unavailable", "no swap route available for this asset, network and amount")
	case errors.Is(err, swap.ErrExpired):
		return responses.Fail(ctx, http.StatusConflict, "swap_quote_expired", "swap quote expired; request a new quote")
	case errors.Is(err, swap.ErrConflict):
		return responses.Fail(ctx, http.StatusConflict, "swap_conflict", "swap conflicts with an existing order")
	case errors.Is(err, swap.ErrProviderRejected):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, "swap_provider_rejected", "swap provider rejected the request")
	case errors.Is(err, swap.ErrProviderUnavailable):
		return responses.Fail(ctx, http.StatusBadGateway, responses.CodeProviderUnavailable, "swap provider unavailable")
	case errors.Is(err, swap.ErrInvalidQuote):
		return responses.Fail(ctx, http.StatusBadGateway, "invalid_swap_quote", "swap provider returned an invalid quote")
	default:
		return responses.InternalError(ctx, err)
	}
}
