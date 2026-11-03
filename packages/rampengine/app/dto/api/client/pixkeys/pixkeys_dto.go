// Package pixkeys contains DTOs for the client saved Pix keys API.
package pixkeys

// CreatePixKeyDTO is the payload for registering a Pix key. An empty
// RecipientDocument means the key is saved without one.
type CreatePixKeyDTO struct {
	Label             string `json:"label"`
	PixKey            string `json:"pix_key"`
	PixKeyType        string `json:"pix_key_type"`
	RecipientName     string `json:"recipient_name"`
	RecipientDocument string `json:"recipient_document"`
} //	@name	ClientCreatePixKeyDTO

// UpdatePixKeyDTO is the payload for editing a saved Pix key. It is a full
// replace: every field is rewritten, and an empty RecipientDocument clears it.
type UpdatePixKeyDTO struct {
	Label             string `json:"label"`
	PixKey            string `json:"pix_key"`
	PixKeyType        string `json:"pix_key_type"`
	RecipientName     string `json:"recipient_name"`
	RecipientDocument string `json:"recipient_document"`
} //	@name	ClientUpdatePixKeyDTO

// PixKeyDTO is a saved Pix key, decrypted. RecipientDocument is nil when the
// key was saved without one.
type PixKeyDTO struct {
	ID                uint    `json:"id"`
	Label             string  `json:"label"`
	PixKey            string  `json:"pix_key"`
	PixKeyType        string  `json:"pix_key_type"`
	RecipientName     string  `json:"recipient_name"`
	RecipientDocument *string `json:"recipient_document,omitempty"`
} //	@name	ClientPixKeyDTO
