package chains

import (
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/egress"
)

// rpcDestination refuses the URL a chain RPC write would dial when egress.RPCPolicy
// refuses it (link-local and cloud metadata destinations unless allowLinkLocal):
// the URL itself, or the value an env:NAME reference names here. A reference that
// does not resolve here is left to the checks that follow. The refusal is the
// policy's sentence and never carries the URL.
func rpcDestination(allowLinkLocal bool, value string) error {
	if dial, err := models.DialEndpoint(value); err == nil {
		return egress.RPCPolicy(allowLinkLocal).CheckURL(dial)
	}
	return nil
}
