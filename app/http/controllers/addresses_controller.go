package controllers

// GenerateAddressRequest is the request body for generating a deposit address.
type GenerateAddressRequest struct {
	ExternalUserID string `json:"external_user_id" example:"user_123"`
	Metadata       string `json:"metadata"          example:"{\"tier\":\"premium\"}"`
}
