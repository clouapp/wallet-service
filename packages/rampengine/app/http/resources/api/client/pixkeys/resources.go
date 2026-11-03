package pixkeys

import dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/pixkeys"

// ClientPixKeyResource is a saved Pix key as returned to its own owner.
// recipient_document is intentionally unmasked here — unlike the masked
// pix_key shown in order history (ClientRampOrderResource) — because the
// caller is the key's own owner fetching it for CRUD (edit/verify), not a
// third party viewing a past transaction.
type ClientPixKeyResource struct {
	ID                uint    `json:"id"`
	Label             string  `json:"label"`
	PixKey            string  `json:"pix_key"`
	PixKeyType        string  `json:"pix_key_type"`
	RecipientName     string  `json:"recipient_name"`
	RecipientDocument *string `json:"recipient_document,omitempty"`
} //	@name	ClientPixKeyResource

// NewPixKeyResource maps a saved Pix key to the wire shape.
func NewPixKeyResource(key *dto.PixKeyDTO) ClientPixKeyResource {
	return ClientPixKeyResource{
		ID: key.ID, Label: key.Label, PixKey: key.PixKey, PixKeyType: key.PixKeyType,
		RecipientName: key.RecipientName, RecipientDocument: key.RecipientDocument,
	}
}

// NewPixKeyCollection maps a list of saved keys for GET /api/pix-keys.
func NewPixKeyCollection(keys []dto.PixKeyDTO) []ClientPixKeyResource {
	items := make([]ClientPixKeyResource, 0, len(keys))
	for i := range keys {
		items = append(items, NewPixKeyResource(&keys[i]))
	}
	return items
}
