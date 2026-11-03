package cms

import (
	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/errors"

	"github.com/macrowallets/waas/packages/rampengine/packages/cms/faq"
	"github.com/macrowallets/waas/packages/rampengine/packages/cms/i18n"
)

const (
	// Binding is the container key the manager is bound under, and the one
	// facades.CMS resolves.
	Binding = "xip.cms"
	// Name is the module the package's errors are tagged with.
	Name = "cms"
)

// App is the application the ServiceProvider registered against.
var App foundation.Application

type ServiceProvider struct{}

// Relationship returns the relationship of the service provider.
func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			Binding,
		},
		Dependencies: []string{
			binding.Config,
			binding.Orm,
		},
		ProvideFor: []string{},
	}
}

// Register binds the manager. The locales are read and checked once, when the
// container first resolves it: a broken cms config fails the boot, not a
// request.
func (r *ServiceProvider) Register(app foundation.Application) {
	App = app

	app.Singleton(Binding, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(Name)
		}
		orm := app.MakeOrm()
		if orm == nil {
			return nil, errors.OrmFacadeNotSet.SetModule(Name)
		}

		supported, _ := config.Get("cms.locales").([]string)
		locales, err := i18n.NewLocales(supported, config.GetString("cms.default_locale"), config.GetString("app.fallback_locale"))
		if err != nil {
			return nil, err
		}

		return NewManager(locales, faq.NewService(orm, locales)), nil
	})
}

// Boot boots the service provider, will be called after all service providers are registered.
func (r *ServiceProvider) Boot(app foundation.Application) {}
