package config

import (
	"golang.org/x/crypto/bcrypt"
)

// hashingDocument is the hash facade's config. The cost is bcrypt.DefaultCost,
// the cost every stored password hash was made with, so NeedsRehash does not
// fire for existing users. Goravel's own default is 12. registerHashing, in
// auth.go, adds it to the config store.
func hashingDocument() map[string]any {
	return map[string]any{
		"driver": "bcrypt",
		"bcrypt": map[string]any{
			"rounds": bcrypt.DefaultCost,
		},
	}
}
