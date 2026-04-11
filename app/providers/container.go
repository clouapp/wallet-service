package providers

import (
	"github.com/macrowallets/waas/app/container"
)

// Boot resolves the WaaS container from the Goravel service container.
func Boot() *container.Container {
	return container.Get()
}

// Get returns the global container instance (delegated to container package).
func Get() *container.Container {
	return container.Get()
}
