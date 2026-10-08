package config

import (
	"golang.org/x/crypto/bcrypt"

	"github.com/goravel/framework/facades"
)

// registerHashing configures the hash facade. The cost is bcrypt.DefaultCost,
// the cost every stored password hash was made with, so NeedsRehash does not
// fire for existing users. Goravel's own default is 12.
func registerHashing() {
	facades.Config().Add("hashing", hashingDocument())
}

func hashingDocument() map[string]any {
	return map[string]any{
		"driver": "bcrypt",
		"bcrypt": map[string]any{
			"rounds": bcrypt.DefaultCost,
		},
	}
}
