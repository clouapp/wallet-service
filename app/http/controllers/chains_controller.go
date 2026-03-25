package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
)

// ListChains godoc
// @Summary      List supported chains
// @Description  Returns all supported blockchain networks with their native asset and confirmation requirements
// @Tags         Chains
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  ChainListResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/chains [get]
func ListChains(ctx http.Context) http.Response {
	type info struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		NativeAsset   string `json:"native_asset"`
		Confirmations uint64 `json:"required_confirmations"`
	}
	var chains []info
	for _, id := range container.Get().Registry.ChainIDs() {
		a, _ := container.Get().Registry.Chain(id)
		chains = append(chains, info{
			ID:            a.ID(),
			Name:          a.Name(),
			NativeAsset:   a.NativeAsset(),
			Confirmations: a.RequiredConfirmations(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": chains,
	})
}
