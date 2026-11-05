package requests

import contractshttp "github.com/goravel/framework/contracts/http"

// PathExternalID is the form request of an endpoint whose only input is an
// opaque {id} in the path, such as a UUID. Its shape is not checked here: an
// id that cannot exist answers 404 from the lookup, as one that does not.
type PathExternalID struct {
	Open
	ID string `form:"id" json:"id"`
}

func (r *PathExternalID) Rules(contractshttp.Context) map[string]string {
	return map[string]string{"id": "required"}
}
