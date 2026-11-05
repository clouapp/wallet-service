package responses

// ValidationErrorResponse is the shape returned when form validation fails.
// Errors maps field name → rule name → message, matching Goravel's validation.Errors.All().
type ValidationErrorResponse struct {
	Error  string                       `json:"error" example:"validation failed"`
	Errors map[string]map[string]string `json:"errors"`
}
