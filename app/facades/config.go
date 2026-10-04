package facades

import (
	"github.com/goravel/framework/contracts/config"
	goravelfacades "github.com/goravel/framework/facades"
)

// Config returns the process configuration. Callers keep the same keys and values.
func Config() config.Config {
	return goravelfacades.Config()
}
