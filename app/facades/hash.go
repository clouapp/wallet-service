package facades

import (
	"github.com/goravel/framework/contracts/hash"
	goravelfacades "github.com/goravel/framework/facades"
)

// Hash returns the process password hasher. Callers keep the same Make, Check and NeedsRehash calls.
func Hash() hash.Hash {
	return goravelfacades.Hash()
}
