package settings

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
)

// UpdateRequest is the body of PUT /platform/settings/{group} and of
// PUT /platform/accounts/{accountId}/settings/{group}: a partial settings
// group. The service decides which keys belong to the group.
type UpdateRequest struct {
	Document map[string]any
}

// Decode reads the partial group. An empty body is an empty document: the
// client omits a secret it does not have. Numbers keep their written form.
func (r *UpdateRequest) Decode(ctx http.Context) error {
	document, err := requests.AccountSettingsDocument(ctx)
	if err != nil {
		return err
	}
	r.Document = document
	return nil
}

// UpdateDocument is the partial group of a write the settings service
// authorizes first, read from the body only when the service asks for it.
type UpdateDocument struct {
	ctx http.Context
}

// NewUpdateDocument reads the partial group from ctx's body on demand.
func NewUpdateDocument(ctx http.Context) UpdateDocument {
	return UpdateDocument{ctx: ctx}
}

// Read decodes UpdateRequest. A body over the limit or one that does not
// decode is the error Decode returned.
func (d UpdateDocument) Read() (map[string]any, error) {
	var req UpdateRequest
	if err := req.Decode(d.ctx); err != nil {
		return nil, err
	}
	return req.Document, nil
}
