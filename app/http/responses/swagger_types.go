package responses

// ErrorBody documents the failure envelope for the Swagger annotations
// (@Failure ... responses.ErrorBody). The handlers never build it: the body on
// the wire is resources.ErrorEnvelope, plus the "errors" map on a 422.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code" example:"not_found"`
		Message string `json:"message" example:"not found"`
	} `json:"error"`
	Errors map[string][]string `json:"errors,omitempty"`
}
