package settings

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
)

// MailTestRequest is the body of POST /platform/settings/mail/test.
// S1.4.6 does not name the body. The recipient field is to, the field
// the platform settings controller validates.
type MailTestRequest struct {
	To string `form:"to" json:"to" example:"ops@example.com"`
}

func (r *MailTestRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *MailTestRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"to": "trim",
	}
}

func (r *MailTestRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"to.required": "Email address is required",
		"to.email":    "Please provide a valid email address",
	}
}

func (r *MailTestRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"to": "required|email",
	}
}

// MailTestRecipient is the to of POST /platform/settings/mail/test, read from
// the body only when the settings service asks for it: a caller who may not
// send the test is refused before the body is read.
type MailTestRecipient struct {
	ctx http.Context
}

// NewMailTestRecipient reads the recipient from ctx's body on demand.
func NewMailTestRecipient(ctx http.Context) MailTestRecipient {
	return MailTestRecipient{ctx: ctx}
}

// Read runs MailTestRequest. A body that does not validate is a
// *requests.Refusal carrying the answer requests.Validate gave.
func (r MailTestRecipient) Read() (string, error) {
	var req MailTestRequest
	if response := requests.Validate(r.ctx, &req); response != nil {
		return "", &requests.Refusal{Response: response}
	}
	return req.To, nil
}
