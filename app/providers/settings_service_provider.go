package providers

import (
	"strings"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/egress"
)

// SettingsServiceProvider binds the account settings store and service, and the
// guard of the ingest base URL, which the settings write and ingest:subscribe share.
type SettingsServiceProvider struct{}

func (p *SettingsServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.SettingRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewSettingRepository(nil), nil
	})
	app.Singleton((*egress.PublicBaseGuard)(nil), func(foundation.Application) (any, error) {
		return ingestBaseDestinations(), nil
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
		baseURLs, err := resolve[*egress.PublicBaseGuard](app)
		if err != nil {
			return nil, err
		}
		return settings.NewService(settings.Deps{Store: store, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: activityLog,
			Fallback: platformSettingsFallback(facades.Config()), IngestBaseDestinations: baseURLs,
			WriteChecks: chainProviderWriteChecks(RPCDestinations())}).
			WithPlatformAdmins(admins).
			WithAccounts(accounts).
			WithPlatformTestMailer(NewPlatformTestMailer()), nil
	})
}

func (p *SettingsServiceProvider) Boot(foundation.Application) {}

// platformSettingsFallback shows, on the platform views, the environment value of a
// platform field that takes it while no row stores it: the chains of
// deposit_signatures (DEPOSIT_SIGNATURE_CHAINS) and the scanned chains of
// local_workers (LOCAL_DEPOSIT_SCAN_CHAINS). A form that sends the whole group
// back then stores the chains in effect, and an env-listed chain is never turned off
// by a save that did not mean to change it; sending an empty list still does.
func platformSettingsFallback(cfg configReader) settings.PlatformFallback {
	return func(group, key string) (any, bool) {
		switch {
		case group == settings.GroupDepositSignatures && key == settings.KeySignatureChains:
			return signatureSettingsFromEnv(cfg).Chains, true
		case group == settings.GroupLocalWorkers && key == settings.KeyDepositScanChains:
			return parseChainList(cfg.GetString("vault.local_workers.deposit_scan_chains")), true
		}
		return nil, false
	}
}

// parseChainList splits a comma-separated chain list, without blanks.
func parseChainList(raw string) []string {
	chains := []string{}
	for _, part := range strings.Split(raw, ",") {
		if chainID := strings.TrimSpace(part); chainID != "" {
			chains = append(chains, chainID)
		}
	}
	return chains
}
