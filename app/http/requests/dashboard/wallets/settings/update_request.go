package settings

import (
	"bytes"
	"errors"
	"io"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/services/walletsettings"
)

var (
	// ErrBodyRequired is the refusal of a settings update that has no body.
	ErrBodyRequired = errors.New("request body is required")
	// ErrBodyUnreadable is the refusal of a body that could not be read.
	ErrBodyUnreadable = errors.New("request body could not be read")
)

// UpdateRequest is the raw JSON body of a settings update. Each field may be
// absent (unchanged), null (reset to the default) or a value, which a form
// request cannot tell apart, so walletsettings.Parse reads the bytes.
type UpdateRequest struct {
	Body []byte
}

// ReadUpdateRequest reads the body, at most walletsettings.MaxBodyBytes (one
// more byte is kept, so an oversized body is refused by the parser, not
// truncated), and leaves it readable for whatever reads it next.
func ReadUpdateRequest(ctx http.Context) (UpdateRequest, error) {
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return UpdateRequest{}, ErrBodyRequired
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, walletsettings.MaxBodyBytes+1))
	if err != nil {
		return UpdateRequest{}, ErrBodyUnreadable
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return UpdateRequest{Body: body}, nil
}
