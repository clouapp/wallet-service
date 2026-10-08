package auth

import (
	"github.com/goravel/framework/config"
	"github.com/goravel/framework/contracts/hash"
	frameworkhash "github.com/goravel/framework/hash"
	"github.com/goravel/framework/support"
	"golang.org/x/crypto/bcrypt"
)

// testHasher is the framework hasher the hashing config builds: bcrypt at DefaultCost.
func testHasher() hash.Hash {
	support.DontVerifyAppKey = true
	cfg := config.NewApplication("")
	cfg.Add("hashing", map[string]any{
		"driver": "bcrypt",
		"bcrypt": map[string]any{"rounds": bcrypt.DefaultCost},
	})
	return frameworkhash.NewApplication(cfg)
}
