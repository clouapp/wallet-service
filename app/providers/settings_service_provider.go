package providers

import (
	"context"

	"github.com/goravel/framework/contracts/foundation"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
)

// SettingsServiceProvider binds the account settings store and service.
type SettingsServiceProvider struct{}

func (p *SettingsServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.SettingRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewSettingRepository(nil), nil
	})
	app.Singleton((*settings.Service)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.SettingRepository](app)
		if err != nil {
			return nil, err
		}
		activityLog, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		admins, err := resolve[*repositories.PlatformAdminRepository](app)
		if err != nil {
			return nil, err
		}
		accounts, err := resolve[*repositories.AccountRepository](app)
		if err != nil {
			return nil, err
		}
		return settings.NewService(settings.Deps{Store: store, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: activityLog}).
			WithPlatformAdmins(admins).
			WithAccounts(accounts).
			WithPlatformTestMailer(platformTestMailer{}), nil
	})
	bindMailFacade(app)
}

func (p *SettingsServiceProvider) Boot(app foundation.Application) {
	svc, err := resolve[*settings.Service](app)
	if err != nil {
		panic("settings: mail smtp reader: " + err.Error())
	}
	appfacades.SetMailSMTPReader(mailSMTPReader(svc))
	appfacades.SetMailFromReader(mailFromReader(svc))
}

func mailSMTPReader(svc *settings.Service) appfacades.MailDialReader {
	return func(ctx context.Context) (appfacades.MailDial, error) {
		got, err := svc.EffectiveMailSMTP(ctx)
		if err != nil {
			return appfacades.MailDial{}, err
		}
		return appfacades.MailDial{
			Host:          got.Host,
			Port:          got.Port,
			Encryption:    got.Encryption,
			Username:      got.Username,
			Password:      got.Password,
			UseHost:       got.UseHost,
			UsePort:       got.UsePort,
			UseEncryption: got.UseEncryption,
			UseUsername:   got.UseUsername,
			UsePassword:   got.UsePassword,
		}, nil
	}
}

func mailFromReader(svc *settings.Service) appfacades.MailFromReader {
	return func(ctx context.Context) (appfacades.MailFrom, error) {
		got, err := svc.EffectiveMailDelivery(ctx)
		if err != nil {
			return appfacades.MailFrom{}, err
		}
		return appfacades.MailFrom{
			Address:    got.Address,
			Name:       got.Name,
			UseAddress: got.UseAddress,
			UseName:    got.UseName,
		}, nil
	}
}
