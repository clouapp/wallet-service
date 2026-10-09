package settings

import (
	"time"

	"github.com/goravel/framework/contracts/http"
)

// FreezeRequest is the body of a wallet freeze: when it ends, as an RFC 3339
// timestamp. Left out, the freeze lasts a default period.
type FreezeRequest struct {
	FrozenUntil string `form:"frozen_until" json:"frozen_until,omitempty"`
}

func (r *FreezeRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *FreezeRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"frozen_until": "rfc3339",
	}
}

// Until is the end of the freeze, or nil when the request names none. The rule
// has already refused anything that is not an RFC 3339 timestamp.
func (r *FreezeRequest) Until() *time.Time {
	if r.FrozenUntil == "" {
		return nil
	}
	until, err := time.Parse(time.RFC3339, r.FrozenUntil)
	if err != nil {
		return nil
	}
	return &until
}
