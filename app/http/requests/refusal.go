package requests

import "github.com/goravel/framework/contracts/http"

// Refusal is a form request's answer carried as an error, for input a service
// reads only when it needs it (the proof that turns 2FA off). The handler
// returns Response as it is, so the answer is the one Validate wrote.
type Refusal struct {
	Response http.Response
}

func (r *Refusal) Error() string {
	return "the request was refused"
}
