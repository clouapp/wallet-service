package ramps

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	erepo "github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
	banksvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/bank"
	customerssvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/customers"
	integration "github.com/macrowallets/waas/packages/rampengine/app/services/client/integration"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	pixsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	lifecycle "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

type Deps struct {
	State                        StateStore
	AppEnvironment               string
	ProviderBaseURLs             map[string]string
	IntentMaterialRetentionHours int
	Decrypt                      func(string) (string, error)
	Activity                     activity.Writer
	Settings                     *settings.Service
	Features                     *features.Service
	Customers                    CustomerStore
	Tasks                        *Tasks
	Wallets                      WalletStore
	Addresses                    AddressStore
	Chains                       ChainStore
	Withdraw                     *withdraw.Service
	Sweep                        *sweep.Box
	Verifier                     *authsvc.SecondFactorVerifier
	Factory                      func(SettingsReader) *ramp.Factory
}
type Service struct{ d Deps }

func NewService(d Deps) *Service {
	if d.Factory == nil {
		d.Factory = buildFactory
	}
	return &Service{d: d}
}

type Runtime struct {
	Factory              *ramp.Factory
	Config               *ramp.Config
	Integration          *integration.Service
	Kyc                  *kycsvc.ProviderKycService
	Capabilities         *kycsvc.CapabilitiesService
	Onramp               *rampsvc.OnrampService
	Offramp              *rampsvc.OfframpService
	VirtualAccounts      *rampsvc.VirtualAccountService
	LiquidationAddresses *rampsvc.LiquidationAddressService
	BankAccounts         *rampsvc.BankAccountService
	BankBeneficiaries    *banksvc.Service
	PixKeys              *pixsvc.Service
	Wallets              *rampsvc.WalletService
	Processor            *lifecycle.WebhookProcessor
	Receiver             *lifecycle.WebhookReceiver
	Reconciler           *lifecycle.Reconciler
	AccountSyncer        *lifecycle.VirtualAccountSyncer
	Finalizations        *rampsvc.FinalizationRecovery
	KycReconciler        *kycsvc.Reconciler
	Mode                 string
}

func ModeForAccount(account *models.Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("ramp runtime requires an account")
	}
	switch account.Environment {
	case "test":
		return "sandbox", nil
	case "prod":
		return "production", nil
	default:
		return "", fmt.Errorf("unsupported account environment")
	}
}

func (s *Service) Runtime(account *models.Account, actor *models.User) (*Runtime, error) {
	mode, err := ModeForAccount(account)
	if err != nil {
		return nil, err
	}
	return s.RuntimeMode(account.ID, mode, actor)
}

// RuntimeMode is also used by background tasks with the verified delivery's
// environment. No process-global provider mode or mutable tenant is used.
func (s *Service) RuntimeMode(accountID uuid.UUID, mode string, actor *models.User) (*Runtime, error) {
	if mode != "sandbox" && mode != "production" {
		return nil, fmt.Errorf("invalid ramp environment")
	}
	reader := SettingsReader{Service: s.d.Settings, Mode: mode, AppEnvironment: s.d.AppEnvironment, BaseURLs: s.d.ProviderBaseURLs}
	factory := s.d.Factory(reader)
	config := ramp.NewConfig(reader)
	appEnv := "testing"
	if mode == "production" {
		appEnv = "production"
	}
	flags := &Flags{Native: s.d.Features, AccountID: accountID, Customers: s.d.Customers}
	locker := locking.NewAdvisoryLocker()
	customers := erepo.NewCustomerRepository()
	profiles := erepo.NewCustomerIndividualProfileRepository()
	businesses := erepo.NewCustomerBusinessProfileRepository()
	members := erepo.NewCustomerMemberRepository()
	integrations := erepo.NewCustomerProviderIntegrationRepositoryForEnvironment(mode)
	kycs := erepo.NewCustomerProviderKycRepository()
	persons := ramp.Persons{Customers: customers, Profiles: profiles, Businesses: customerssvc.NewCompanyPersons(businesses)}
	router := ramp.NewRouter(ramp.RouterDeps{Factory: factory, Config: config, Integrations: integrations, Kyc: kycs, Persons: persons, AppEnv: appEnv})
	links := integration.NewService(factory, integrations, locker)
	events := newEventSink(s.d.Customers, s.d.State, customers, kycs)
	kyc := kycsvc.NewProviderKycService(kycsvc.Deps{Factory: factory, Config: config, Linker: links, Kyc: kycs, Profiles: profiles, Persons: persons, Integrations: integrations, Members: members, Staging: kycsvc.NewCacheStaging(), Dispatcher: KycDispatcher{Tasks: s.d.Tasks, AccountID: accountID, Mode: mode}, Locker: locker, Broadcaster: events, Notifier: events, AppEnv: appEnv, Features: flags, Businesses: businesses, Customers: customers, Users: erepo.NewUserRepository(), Activator: events, Timeline: lifecycle.NewProviderTimeline(erepo.NewProviderCallRepository()), Intents: erepo.NewProviderKycSubmissionIntentRepositoryForTenant(accountID.String(), mode), IntentMaterialRetention: kycsvc.IntentMaterialRetention(s.d.IntentMaterialRetentionHours)})
	caps := kycsvc.NewCapabilitiesService(kycsvc.CapabilitiesDeps{Resolver: router, Features: flags, Config: config, Factory: factory, Kyc: kycs, Integrations: integrations, Persons: persons, Steps: kyc, AppEnv: appEnv})
	transactions := erepo.NewTransactionRepositoryForTenant(accountID.String(), mode)
	accounts := erepo.NewCustomerVirtualAccountRepositoryForTenant(accountID.String(), mode)
	banks := erepo.NewUserBankAccountRepository()
	bankDestinations := erepo.NewBankAccountDestinationRepository()
	addresses := erepo.NewUserLiquidationAddressRepository()
	destinations := erepo.NewOfframpDestinationRepository()
	funding := erepo.NewOfframpFundingAuthorizationRepositoryForTenant(accountID.String(), mode)
	beneficiaries := erepo.NewCustomerBankBeneficiaryRepository()
	registeredBanks := banksvc.NewProviderAccountService(banksvc.ProviderAccountDeps{Beneficiaries: beneficiaries, Accounts: erepo.NewBankProviderAccountRepository(), Integrations: integrations, Factory: factory, Locker: locker})
	mfa := OperatorMFA{Actor: actor, Verifier: s.d.Verifier}
	d := rampsvc.Deps{Router: router, Config: config, Transactions: transactions, Estimates: erepo.NewRampEstimateRepository(), Locker: locker, Broadcaster: events, Members: members, Notifier: events, Features: flags, PendingSteps: kyc, VirtualAccounts: accounts, Integrations: integrations, Factory: factory, LiquidationAddresses: addresses, BankAccounts: banks, Wallets: erepo.NewCustomerWalletRepository(), Businesses: businesses, Activation: events, BeneficiaryAccounts: registeredBanks, Beneficiaries: banksvc.NewService(beneficiaries), FundingAuthorizations: funding, PixAccounts: rampsvc.NewPixAccountService(destinations, profiles, locker)}
	onramp := rampsvc.NewOnrampService(d)
	offramp := rampsvc.NewOfframpService(d, destinations, mfa, appEnv, func() (queue.Job, error) {
		return settlementJob{Tasks: s.d.Tasks, AccountID: accountID, Mode: mode}, nil
	})
	liquidation := rampsvc.NewLiquidationAddressService(d, banks, bankDestinations, addresses)
	webhooks := environmentEvents{WebhookEventRepository: erepo.NewWebhookEventRepository(), Mode: mode}
	processor := lifecycle.NewWebhookProcessor(lifecycle.WebhookProcessorDeps{Factory: factory, Config: config, Events: webhooks, Integrations: integrations, Accounts: accounts, Members: members, Broadcaster: events, Notifier: events, Features: flags, Deliverer: onramp, Settler: offramp, Bindings: lifecycle.NewBindingSource(funding), Attention: transactions, Addresses: addresses}).WithKycRefresher(kyc)
	return &Runtime{Factory: factory, Config: config, Integration: links, Kyc: kyc, Capabilities: caps, Onramp: onramp, Offramp: offramp, VirtualAccounts: rampsvc.NewVirtualAccountService(d, mfa), LiquidationAddresses: liquidation, BankAccounts: rampsvc.NewBankAccountService(banks, liquidation, mfa, locker), BankBeneficiaries: banksvc.NewService(beneficiaries), PixKeys: pixsvc.NewService(erepo.NewUserPixKeyRepository()), Wallets: rampsvc.NewWalletService(rampsvc.WalletDeps{Wallets: d.Wallets, Accounts: accounts, Approval: kyc, MfaStepUp: mfa, Activation: events}), Processor: processor, Receiver: lifecycle.NewWebhookReceiver(factory, webhooks, EventDispatcher{Tasks: s.d.Tasks, Mode: mode}), Reconciler: lifecycle.NewReconciler(lifecycle.ReconcilerDeps{Router: router, Factory: factory, Integrations: integrations, Transactions: transactions, Accounts: accounts, Notifier: events, Config: config, Deliverer: onramp, Settler: offramp, Addresses: addresses, Audience: broadcast.NewAudience(events, members), Bindings: lifecycle.NewBindingSource(funding)}), AccountSyncer: lifecycle.NewVirtualAccountSyncer(factory, integrations, accounts, config), Finalizations: rampsvc.NewFinalizationRecovery(d), KycReconciler: kycsvc.NewReconciler(kycs, kyc.Refresh), Mode: mode}, nil
}

func (s *Service) Work(ctx context.Context, limit int) (int, error) {
	// Claim just before execution; a batch must not hold leases that can expire
	// while earlier provider calls are still running.
	done := 0
	for attempt := 0; attempt < limit; attempt++ {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		tasks, err := s.d.Tasks.Claim(ctx, 1)
		if err != nil {
			return done, err
		}
		if len(tasks) == 0 {
			break
		}
		task := tasks[0]
		cause := s.runTask(ctx, task)
		if err := s.d.Tasks.Finish(ctx, task.ID, cause); err != nil {
			return done, err
		}
		if cause == nil {
			done++
		}
	}
	return done, nil
}
