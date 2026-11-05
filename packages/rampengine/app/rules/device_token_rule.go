package rules

import (
	"context"
	"regexp"

	"github.com/goravel/framework/contracts/validation"
)

// deviceTokenPattern is what either vendor issues: APNs tokens are hex, and
// FCM tokens add ':', '-' and '_'. It is a rule rather than a regex: in a rule
// string the parser reads a literal ':' as a separator, and an FCM token needs
// one.
var deviceTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_:-]+$`)

// maxDeviceTokenLen bounds a token. Neither vendor promises a length, so it is
// several times what either issues today (64 and about 160 characters) -- and
// well inside what Postgres indexes, since the column carries a unique index
// and a longer value would fail the insert instead of the validation.
const maxDeviceTokenLen = 1024

// DeviceTokenRule accepts a push token as APNs or FCM issues one. Registering
// and unregistering share it, so a token that can be saved can always be
// removed by its owner -- including from the URL path, which is where the
// unregister reads it.
type DeviceTokenRule struct{}

func (r *DeviceTokenRule) Signature() string { return "device_token" }

func (r *DeviceTokenRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	token, ok := val.(string)
	return ok && len(token) <= maxDeviceTokenLen && deviceTokenPattern.MatchString(token)
}

func (r *DeviceTokenRule) Message(_ context.Context) string {
	return "must be a push token as APNs or FCM issues it"
}
