package facades

import (
	"github.com/goravel/framework/contracts/console"
)

// Artisan returns the command runner.
func Artisan() console.Artisan {
	return App().MakeArtisan()
}
