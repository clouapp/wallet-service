package facades

import (
	"github.com/goravel/framework/contracts/log"
	goravelfacades "github.com/goravel/framework/facades"
)

// Log returns the process logger. Callers keep the same levels, messages, and fields.
func Log() log.Log {
	return goravelfacades.Log()
}
