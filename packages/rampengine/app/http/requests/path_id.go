package requests

import contractshttp "github.com/goravel/framework/contracts/http"

// PathID is the form request of an endpoint whose only input is a numeric {id}
// in the path. Such an endpoint uses it rather than a copy of its own.
type PathID struct {
	Open
	ID string `form:"id" json:"id"`
}

func (r *PathID) Rules(contractshttp.Context) map[string]string {
	return map[string]string{"id": "required|numeric|min:1"}
}
