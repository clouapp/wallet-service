package requests

import "github.com/goravel/framework/contracts/http"

// PlatformMailTestRequest is the body of POST /v1/platform/settings/mail/test.
// S1.4.6 does not name the body. The recipient field is to, the field
// SettingController.TestMail validates.
type PlatformMailTestRequest struct {
	To string `form:"to" json:"to"`
}

func (r *PlatformMailTestRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *PlatformMailTestRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"to": "trim",
	}
}

func (r *PlatformMailTestRequest) Messages(ctx http.Context) map[string]string {
	return map[string]string{
		"to.required": "Email address is required",
		"to.email":    "Please provide a valid email address",
	}
}

func (r *PlatformMailTestRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"to": "required|email",
	}
}
