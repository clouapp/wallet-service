// Package container defines shared service-container binding keys, and the
// generic Make/MustMake that resolve them, so both providers (which register
// bindings) and consumers (routes, middleware, commands) can reach a binding
// without creating an import cycle.
package container

const (
	UserRepositoryKey       = "xip.repositories.user"
	AuthServiceKey          = "xip.services.auth"
	WalletRestoreServiceKey = "xip.services.client.wallet_restore"
	ProfileServiceKey       = "xip.services.profile"

	// CustomerMemberRepositoryKey and CustomerServiceKey back the customer a
	// request acts for (middleware.CustomerScope).
	CustomerMemberRepositoryKey = "xip.repositories.customer_member"
	CustomerServiceKey          = "xip.services.client.customers"

	// OTPCodeRepositoryKey backs every subject's one-time codes. The two OTP
	// services below share it and differ only in mail templates and lifetimes.
	OTPCodeRepositoryKey = "xip.repositories.otp_code"
	ClientOTPServiceKey  = "xip.services.otp.client"
	AdminOTPServiceKey   = "xip.services.otp.admin"

	AdminRepositoryKey          = "xip.repositories.admin"
	RoleRepositoryKey           = "xip.repositories.role"
	PermissionRepositoryKey     = "xip.repositories.permission"
	AdminAuthServiceKey         = "xip.services.admin.auth"
	AdminProfileServiceKey      = "xip.services.admin.profile"
	AdminUserServiceKey         = "xip.services.admin.users"
	AdminAdminServiceKey        = "xip.services.admin.admins"
	AdminRoleServiceKey         = "xip.services.admin.roles"
	AdminPermissionServiceKey   = "xip.services.admin.permissions"
	AdminTransactionServiceKey  = "xip.services.admin.transactions"
	AdminFeatureServiceKey      = "xip.services.admin.features"
	AdminProviderServiceKey     = "xip.services.admin.providers"
	AdminKycExplorerServiceKey  = "xip.services.admin.kyc_explorer"
	AdminKycProfileServiceKey   = "xip.services.admin.kyc_profile"
	AdminWebhookEventServiceKey = "xip.services.admin.webhook_events"
	RequestLogRepositoryKey     = "xip.repositories.request_log"
	RequestLogWriterKey         = "xip.services.requestlog.writer"
	AdminRequestLogServiceKey   = "xip.services.admin.request_logs"
	AdminMetricsRepositoryKey   = "xip.repositories.admin_metrics"
	AdminMetricsServiceKey      = "xip.services.admin.metrics"
	AdminPolicyKey              = "xip.policies.admin"
	AdminAuditLogRepositoryKey  = "xip.repositories.admin_audit_log"
	AdminAuditServiceKey        = "xip.services.admin.audit"

	MailerConfigKey  = "xip.providers.mailer.config"
	MailerFactoryKey = "xip.providers.mailer.factory"
	MailerKey        = "xip.services.mail.mailer"
	SendMailJobKey   = "xip.jobs.send_mail"

	SettingRepositoryKey = "xip.repositories.setting"
	SettingsServiceKey   = "xip.services.settings"

	TransactionRepositoryKey   = "xip.repositories.transaction"
	WebhookEventRepositoryKey  = "xip.repositories.webhook_event"
	MfaBackupCodeRepositoryKey = "xip.repositories.mfa_backup_code"
	MfaCredentialRepositoryKey = "xip.repositories.mfa_credential"
	MfaServiceKey              = "xip.services.client.mfa"

	CustomerProviderIntegrationRepositoryKey = "xip.repositories.customer_provider_integration"
	CustomerProviderKycRepositoryKey         = "xip.repositories.customer_provider_kyc"
	CustomerIndividualProfileRepositoryKey   = "xip.repositories.customer_individual_profile"
	UserPixKeyRepositoryKey                  = "xip.repositories.user_pix_key"
	CustomerBankBeneficiaryRepositoryKey     = "xip.repositories.customer_bank_beneficiary"
	BankProviderAccountRepositoryKey         = "xip.repositories.customer_bank_beneficiary_provider_account"
	OfframpFundingAuthorizationRepositoryKey = "xip.repositories.offramp_funding_authorization"
	BankBeneficiaryServiceKey                = "xip.services.client.bank_beneficiaries"
	BankProviderAccountServiceKey            = "xip.services.client.bank_provider_accounts"
	PixAccountServiceKey                     = "xip.services.client.pix_accounts"
	AdminBankBeneficiaryServiceKey           = "xip.services.admin.bank_beneficiaries"
	PixKeysServiceKey                        = "xip.services.client.pix_keys"
	CapabilitiesServiceKey                   = "xip.services.client.kyc_capabilities"
	RampConfigKey                            = "xip.providers.ramp.config"
	RampFactoryKey                           = "xip.providers.ramp.factory"
	RampRouterKey                            = "xip.providers.ramp.router"
	// RampPersonsKey is who each customer declared itself to be, which the
	// router and the KYC listings open rails by.
	RampPersonsKey                      = "xip.providers.ramp.persons"
	IntegrationServiceKey               = "xip.services.client.integration"
	ProviderKycServiceKey               = "xip.services.client.provider_kyc"
	KycStagingKey                       = "xip.services.client.kyc_staging"
	KycReconcilerKey                    = "xip.services.client.kyc_reconciler"
	KycSubmitJobKey                     = "xip.jobs.kyc_submit"
	RampEstimateRepositoryKey           = "xip.repositories.ramp_estimate"
	CustomerVirtualAccountRepositoryKey = "xip.repositories.customer_virtual_account"
	OfframpDestinationRepositoryKey     = "xip.repositories.offramp_destination"
	ProviderCallRepositoryKey           = "xip.repositories.provider_call"
	OnrampServiceKey                    = "xip.services.client.onramp"
	ClientTransactionServiceKey         = "xip.services.client.transactions"
	OfframpServiceKey                   = "xip.services.client.offramp"
	FinalizationRecoveryKey             = "xip.services.client.finalization_recovery"
	VirtualAccountServiceKey            = "xip.services.client.virtual_account"
	RampWebhookProcessorKey             = "xip.services.ramp.webhook_processor"
	RampWebhookJobKey                   = "xip.jobs.ramp_webhook"
	SettleOfframpJobKey                 = "xip.jobs.settle_offramp"
	RampWebhookReceiverKey              = "xip.services.ramp.webhook_receiver"
	RampWebhookSweeperKey               = "xip.services.ramp.webhook_sweeper"
	RampReconcilerKey                   = "xip.services.ramp.reconciler"
	RampAccountSyncerKey                = "xip.services.ramp.account_syncer"
	AdminVirtualAccountServiceKey       = "xip.services.admin.virtual_account"
	AdminAttentionServiceKey            = "xip.services.admin.ramp_attention"
	AdminAttentionActionsKey            = "xip.services.admin.ramp_attention_actions"
	AdminPayoutDestinationServiceKey    = "xip.services.admin.payout_destination"
	UserBankAccountRepositoryKey        = "xip.repositories.user_bank_account"
	BankAccountServiceKey               = "xip.services.client.bank_account"
	BankAccountDestinationRepositoryKey = "xip.repositories.bank_account_destination"
	UserLiquidationAddressRepositoryKey = "xip.repositories.user_liquidation_address"
	LiquidationAddressServiceKey        = "xip.services.client.liquidation_address"

	LockerKey      = "xip.services.locking.locker"
	BroadcasterKey = "xip.services.broadcast"

	PusherConfigKey          = "xip.providers.pusher.config"
	PusherFactoryKey         = "xip.providers.pusher.factory"
	PushServiceKey           = "xip.services.push"
	SendPushJobKey           = "xip.jobs.send_push"
	DeviceTokenRepositoryKey = "xip.repositories.device_token"
	DeviceTokensServiceKey   = "xip.services.client.device_tokens"

	PostalConfigKey      = "xip.providers.postal.config"
	PostalFactoryKey     = "xip.providers.postal.factory"
	PostalRouterKey      = "xip.providers.postal.router"
	PostalCodeServiceKey = "xip.services.client.postal_code"

	CompanyRegistryConfigKey  = "xip.providers.company_registry.config"
	CompanyRegistryFactoryKey = "xip.providers.company_registry.factory"
	CompanyRegistryRouterKey  = "xip.providers.company_registry.router"
	CompanyRegistryServiceKey = "xip.services.client.company_registry"
)

// Kept in a block of its own so adding it does not realign the keys above.
const ProviderKycSubmissionIntentRepositoryKey = "xip.repositories.provider_kyc_submission_intent"
