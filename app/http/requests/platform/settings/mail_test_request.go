package settings

import "github.com/goravel/framework/contracts/http"

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
