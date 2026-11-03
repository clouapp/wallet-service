package responses

// MessageResponse is the shape returned by endpoints that only confirm an action.
type MessageResponse struct {
	Message string `json:"message"`
}
