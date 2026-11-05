package requests

import contractshttp "github.com/goravel/framework/contracts/http"

// Open is the Authorize every form request embeds. Who may call an endpoint is
// route middleware or a Gate policy here, never the form request, so the
// method the FormRequest interface demands always allows. A request that ever
// needs its own check declares Authorize, and the outer method wins.
type Open struct{}

func (Open) Authorize(contractshttp.Context) error { return nil }
