package facades

import (
	"github.com/goravel/framework/contracts/auth/access"
)

func Gate() access.Gate {
	return App().MakeGate()
}
