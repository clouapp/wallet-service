// Package facades is the CMS's facade, imported from here by the app —
// `cmsfacades "github.com/macrowallets/waas/packages/rampengine/packages/cms/facades"` — as Goravel's own packages
// are (goravel/redis's installer writes redisfacades into config/cache.go).
package facades

import (
	"github.com/macrowallets/waas/packages/rampengine/packages/cms"
	"github.com/macrowallets/waas/packages/rampengine/packages/cms/contracts"
)

// CMS resolves the manager the cms ServiceProvider binds.
//
// It panics where the make:package stub logs and returns nil: a nil CMS fails
// on its first call with a nil dereference that names neither the binding nor
// the reason, whereas this fails at the first resolve — boot, for every
// caller wired from the routes — with the error that explains it.
func CMS() contracts.CMS {
	if cms.App == nil {
		panic(cms.ErrServiceProviderNotRegistered)
	}

	instance, err := cms.App.Make(cms.Binding)
	if err != nil {
		panic(err)
	}

	return instance.(contracts.CMS)
}
