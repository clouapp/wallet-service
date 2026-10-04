package activitylog

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/contracts/foundation"
	"gorm.io/gorm"
)

// ServiceProvider registers the manual API and installs the automatic capture.
type ServiceProvider struct{}

// Relationship returns the relationship of the service provider.
func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings:     []string{Binding},
		Dependencies: []string{},
		ProvideFor:   []string{},
	}
}

// Register binds the manual API.
func (r *ServiceProvider) Register(app foundation.Application) {
	App = app

	app.Bind(Binding, func(app foundation.Application) (any, error) {
		return &ActivityLog{}, nil
	})
}

// Boot installs the gorm plugin.
//
// A failure here is FATAL when there is a database to install onto: a build
// where capture silently did not install is the one outcome this feature cannot
// tolerate — every table would look audited and none would be. The panic follows
// DatabaseServiceProvider, which already refuses to boot against an
// uninitialised ORM for the same reason.
//
// No ORM at all is NOT an error. The control plane supports running without a
// database, and cmd/openapi never boots this container in the first
// place; in both cases the honest outcome is "not installed", said out loud,
// rather than a crash in a process that was never going to write an audited row.
func (r *ServiceProvider) Boot(app foundation.Application) {
	o := app.MakeOrm()
	if o == nil {
		slog.Warn("activitylog: no ORM configured; automatic capture is NOT installed")
		return
	}

	query := o.Query()
	if query == nil {
		slog.Warn("activitylog: ORM returned no query; automatic capture is NOT installed")
		return
	}

	aware, ok := query.(gormAware)
	if !ok {
		panic(fmt.Errorf("activitylog: the orm query (%T) no longer exposes Instance() *gorm.DB", query))
	}

	if aware.Instance() == nil {
		slog.Warn("activitylog: ORM query Instance() is nil; automatic capture is NOT installed")
		return
	}

	if err := Install(o); err != nil {
		panic(fmt.Errorf("activitylog: %w", err))
	}
}

// gormAware is the one framework seam this package depends on.
//
// It is declared as an anonymous shape rather than imported, so the package
// reaches gorm and the framework's CONTRACTS and never
// github.com/goravel/framework/database/gorm — which keeps the empty allow-set
// of tests/architecture intact and keeps the dependency to a single method.
// Instance() is exported but absent from contracts/, so it is the piece a
// framework upgrade could move; when it does, this fails at boot with a sentence
// naming it, never silently at runtime.
type gormAware interface {
	Instance() *gorm.DB
}

// Install registers the plugin on the *gorm.DB the framework caches per
// connection. It is idempotent, and the service provider calls it at boot.
//
// One registration covers the whole process: gorm keeps `callbacks` on Config
// and DB embeds *Config, so every Session, every WithContext and every
// transaction share them.
//
// # Why this is exported, and when you must call it again
//
// The registration lives on ONE *gorm.DB, which the framework caches per
// connection name (database/driver/gorm.go: connectionToDB). Anything that
// empties that cache makes the next query run on a REBUILT instance carrying
// only the framework's own plugins — capture silently uninstalled, with no boot
// left to fail.
//
// In goravel v1.18.0 exactly one thing empties it: Orm.Fresh() ->
// driver.ResetConnections(), reached from testing/docker/database.go:70 inside
// Database.Ready(). That is the test harness, so tests/feature's TestMain calls
// Install again after Ready(). Application.Refresh() is not the same path — it
// clears the container and re-boots providers, which re-runs this anyway.
//
// This was not theory: the first end-to-end run captured nothing, and the boot
// log and the test observed two different *gorm.DB pointers. If a future
// framework version grows a second caller of Orm.Fresh(), this is the function
// that has to be called after it.
func Install(o orm.Orm) error {
	if o == nil {
		return errors.New("no orm; automatic capture cannot install")
	}

	query := o.Query()
	if query == nil {
		return errors.New("orm returned no query; automatic capture cannot install")
	}

	aware, ok := query.(gormAware)
	if !ok {
		return fmt.Errorf(
			"the orm query (%T) no longer exposes Instance() *gorm.DB; automatic capture cannot install", query)
	}

	db := aware.Instance()
	if db == nil {
		return errors.New("the orm query exposed a nil *gorm.DB; automatic capture cannot install")
	}

	// gorm refuses a duplicate plugin name with ErrRegistered rather than
	// no-op'ing, so boot idempotence has to be written. It is not hypothetical:
	// anything that rebuilds the container in one process (a test harness, a
	// second Boot) would otherwise take the panic above.
	if err := db.Use(&plugin{}); err != nil && !errors.Is(err, gorm.ErrRegistered) {
		return fmt.Errorf("installing the gorm plugin: %w", err)
	}

	return nil
}
