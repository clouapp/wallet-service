package providers

import (
	"github.com/macrowallets/waas/app/container"
)

// Boot builds the full dependency graph (delegated to container package).
func Boot() *container.Container {
	return container.Boot()
}

// Get returns the global container instance (delegated to container package).
func Get() *container.Container {
	return container.Get()
}
