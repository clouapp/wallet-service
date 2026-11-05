package featureflag

import (
	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/errors"
)

const (
	// Binding is the container key the manager is bound under, and the one
	// facades.Feature resolves.
	Binding = "xip.featureflag"
	// Name is the module the package's errors are tagged with.
	Name = "featureflag"
)

// App is the application the ServiceProvider registered against.
var App foundation.Application

type ServiceProvider struct {
}

// Relationship returns the relationship of the service provider.
func (r *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			Binding,
		},
		Dependencies: []string{
			binding.Config,
			binding.Log,
			binding.Orm,
		},
		ProvideFor: []string{},
	}
}

// Register binds the manager over the store featureflag.default names. It is a
// singleton because the resolvers Define registers live in it.
func (r *ServiceProvider) Register(app foundation.Application) {
	App = app

	app.Singleton(Binding, func(app foundation.Application) (any, error) {
		config := app.MakeConfig()
		if config == nil {
			return nil, errors.ConfigFacadeNotSet.SetModule(Name)
		}

		log := app.MakeLog()
		if log == nil {
			return nil, errors.LogFacadeNotSet.SetModule(Name)
		}

		driver, err := NewDriverCreator(config, log, app.MakeOrm()).Create(config.GetString("featureflag.default"))
		if err != nil {
			return nil, err
		}

		return NewManager(driver), nil
	})
}

// Boot boots the service provider, will be called after all service providers are registered.
func (r *ServiceProvider) Boot(app foundation.Application) {

}
