package facades

import (
	"github.com/goravel/framework/contracts/crypt"
	goravelfacades "github.com/goravel/framework/facades"
)

// Crypt returns the process cipher. Callers keep the same encrypt and decrypt calls.
func Crypt() crypt.Crypt {
	return goravelfacades.Crypt()
}
