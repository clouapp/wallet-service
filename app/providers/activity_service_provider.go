package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/activity"
)

// ActivityServiceProvider binds the account activity repository and the reader
// used by GET /v1/accounts/{accountId}/activity. The same repository is the
// writer injected into member, settings and feature-flag services.
type ActivityServiceProvider struct{}

func (p *ActivityServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.AccountActivityRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewAccountActivityRepository(nil), nil
	})
	app.Singleton((*activity.Service)(nil), func(app foundation.Application) (any, error) {
		rows, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		admins, err := resolve[*repositories.PlatformAdminRepository](app)
		if err != nil {
			return nil, err
		}
		return activity.NewService(rows).WithPlatformAdmins(admins), nil
	})
}

func (p *ActivityServiceProvider) Boot(foundation.Application) {}
