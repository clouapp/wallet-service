package facades

import (
	"github.com/goravel/framework/contracts/auth/access"
)

// Gate returns the authorization gate.
func Gate() access.Gate {
	return App().MakeGate()
}
