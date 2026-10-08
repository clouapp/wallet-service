package facades

import (
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"
)

// App returns the booted application.
func App() contractsfoundation.Application {
	return foundation.App
}
