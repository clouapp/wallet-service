package pixkeys

import (
	"errors"

	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/http/middleware"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	pixkeysrequests "github.com/macrowallets/waas/packages/rampengine/app/http/requests/api/client/pixkeys"
	resources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	pixkeyssvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
)

// PixKeysController manages saved Pix keys for the authenticated user.
type PixKeysController struct {
	service *pixkeyssvc.Service
}

// NewPixKeysController wires the controller.
func NewPixKeysController(service *pixkeyssvc.Service) *PixKeysController {
	return &PixKeysController{service: service}
}

// pathID validates the {id} segment and parses it. The only value the numeric
// rule lets through that ParseID refuses is one past uint64, which names no
// key: it answers like any other missing one.
func pathID(ctx contractshttp.Context) (uint, contractshttp.Response) {
	var req requests.PathID
	if response := requests.ValidatePath(ctx, &req); response != nil {
		return 0, response
	}
	id, ok := requests.ParseID(req.ID)
	if !ok {
		return 0, responses.Error(ctx, contractshttp.StatusNotFound, "not_found")
	}
	return id, nil
}

func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	switch {
	case errors.Is(err, pixkeyssvc.ErrPixKeyNotFound):
		return responses.Error(ctx, contractshttp.StatusNotFound, "not_found")
	case errors.Is(err, pixkeyssvc.ErrDuplicatePixKey):
		return responses.Error(ctx, contractshttp.StatusConflict, "duplicate_pix_key")
	}
	facades.Log().WithContext(ctx).Errorf("pixkeys: %s: %v", action, err)
	return responses.Error(ctx, contractshttp.StatusInternalServerError, "internal_error")
}

// Index godoc
//
//	@Summary	List the authenticated user's saved Pix keys
//	@Tags		pix-keys
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{array}		resources.ClientPixKeyResource
//	@Failure	401	{object}	responses.ErrorResponse
//	@Failure	500	{object}	responses.ErrorResponse
//	@Router		/api/pix-keys [get]
func (c *PixKeysController) Index(ctx contractshttp.Context) contractshttp.Response {
	userID := middleware.CurrentUser(ctx).ID
	keys, err := c.service.List(userID)
	if err != nil {
		return mapError(ctx, err, "list pix keys")
	}
	return ctx.Response().Success().Json(resources.NewPixKeyCollection(keys))
}

// Store godoc
//
//	@Summary	Register a new Pix key
//	@Tags		pix-keys
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		request	body		pixkeysrequests.CreatePixKeyRequest	true	"Pix key"
//	@Success	201		{object}	resources.ClientPixKeyResource
//	@Failure	401		{object}	responses.ErrorResponse
//	@Failure	409		{object}	responses.ErrorResponse	"duplicate_pix_key"
//	@Failure	422		{object}	responses.ValidationErrorResponse
//	@Failure	500		{object}	responses.ErrorResponse
//	@Router		/api/pix-keys [post]
func (c *PixKeysController) Store(ctx contractshttp.Context) contractshttp.Response {
	var req pixkeysrequests.CreatePixKeyRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	userID := middleware.CurrentUser(ctx).ID
	key, err := c.service.Create(userID, dto.CreatePixKeyDTO{
		Label:             req.Label,
		PixKey:            req.PixKey,
		PixKeyType:        req.PixKeyType,
		RecipientName:     req.RecipientName,
		RecipientDocument: req.RecipientDocument,
	})
	if err != nil {
		return mapError(ctx, err, "create pix key")
	}
	return ctx.Response().Status(contractshttp.StatusCreated).Json(resources.NewPixKeyResource(key))
}

// Update godoc
//
//	@Summary	Edit a saved Pix key
//	@Tags		pix-keys
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id		path		int									true	"Pix key id"
//	@Param		request	body		pixkeysrequests.UpdatePixKeyRequest	true	"Pix key"
//	@Success	200		{object}	resources.ClientPixKeyResource
//	@Failure	401		{object}	responses.ErrorResponse
//	@Failure	404		{object}	responses.ErrorResponse
//	@Failure	409		{object}	responses.ErrorResponse	"duplicate_pix_key"
//	@Failure	422		{object}	responses.ValidationErrorResponse
//	@Failure	500		{object}	responses.ErrorResponse
//	@Router		/api/pix-keys/{id} [put]
func (c *PixKeysController) Update(ctx contractshttp.Context) contractshttp.Response {
	id, response := pathID(ctx)
	if response != nil {
		return response
	}
	var req pixkeysrequests.UpdatePixKeyRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}
	userID := middleware.CurrentUser(ctx).ID
	key, err := c.service.Update(userID, id, dto.UpdatePixKeyDTO{
		Label:             req.Label,
		PixKey:            req.PixKey,
		PixKeyType:        req.PixKeyType,
		RecipientName:     req.RecipientName,
		RecipientDocument: req.RecipientDocument,
	})
	if err != nil {
		return mapError(ctx, err, "update pix key")
	}
	return ctx.Response().Success().Json(resources.NewPixKeyResource(key))
}

// Destroy godoc
//
//	@Summary	Delete a saved Pix key
//	@Tags		pix-keys
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path	int	true	"Pix key id"
//	@Success	204
//	@Failure	401	{object}	responses.ErrorResponse
//	@Failure	404	{object}	responses.ErrorResponse
//	@Failure	422	{object}	responses.ValidationErrorResponse
//	@Failure	500	{object}	responses.ErrorResponse
//	@Router		/api/pix-keys/{id} [delete]
func (c *PixKeysController) Destroy(ctx contractshttp.Context) contractshttp.Response {
	id, response := pathID(ctx)
	if response != nil {
		return response
	}
	userID := middleware.CurrentUser(ctx).ID
	if err := c.service.Delete(userID, id); err != nil {
		return mapError(ctx, err, "delete pix key")
	}
	return ctx.Response().NoContent(contractshttp.StatusNoContent)
}
