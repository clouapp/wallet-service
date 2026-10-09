package webhooks

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/webhook"
)

// The action each handler labels its failures with.
const (
	actionStore  = "create webhook config"
	actionIndex  = "list webhook configs"
	actionUpdate = "update_webhook"
)

// mapError turns a webhook service failure into the answer the external API
// has always given. Update keeps the shared internal-error body, Store and
// Index keep theirs; the cause stays in the log.
func mapError(ctx http.Context, err error, action string) http.Response {
	switch {
	case errors.Is(err, webhook.ErrWebhookConfigNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, webhook.ErrWebhookConfigNotFound.Error())
	case errors.Is(err, webhook.ErrWebhookOwnershipNotProven):
		return responses.Error(ctx, http.StatusForbidden, responses.CodeForbidden, webhook.ErrWebhookOwnershipNotProven.Error())
	case errors.Is(err, webhook.ErrWebhookUpdateEmpty):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, webhook.ErrWebhookUpdateEmpty.Error())
	case errors.Is(err, webhook.ErrWebhookEventsEmpty):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, webhook.ErrWebhookEventsEmpty.Error())
	case errors.Is(err, webhook.ErrWebhookUnknownEvent):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, webhook.ErrWebhookUnknownEvent.Error())
	case action == actionUpdate:
		return controllers.MapInternalError(ctx, err, action)
	}
	slog.Error(action+" failed", "error_type", fmt.Sprintf("%T", err))
	return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
}
