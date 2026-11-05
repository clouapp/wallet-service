package container

// The business half of customers: who holds the money besides the person who
// signs in (KYB spec §3.1, §6.1).
const (
	CustomerRepositoryKey                = "xip.repositories.customer"
	CustomerBusinessProfileRepositoryKey = "xip.repositories.customer_business_profile"
	// CustomerWalletRepositoryKey backs the registered addresses: POST
	// /api/wallets writes them and the money rules of a business read them.
	CustomerWalletRepositoryKey = "xip.repositories.customer_wallet"
	// CustomerDirectoryKey backs GET and POST /api/customers.
	CustomerDirectoryKey = "xip.services.client.customers.directory"
	// WalletServiceKey backs POST /api/wallets.
	WalletServiceKey = "xip.services.client.ramps.wallets"
	// KycNotifierKey is the pushsvc.Notifier the KYC flows and the webhooks
	// tell of a change: customers.KycVerdicts, which activates a business on
	// its first approval and tells its members, and hands the rest to push.
	KycNotifierKey = "xip.services.client.customers.kyc_verdicts"
	// CustomerPolicyKey is the one reader of a customer's RBAC (spec §4).
	CustomerPolicyKey = "xip.policies.customer"
	// AdminCustomerServiceKey backs the admin's customers: the Clientes
	// listing, the Cliente 360 and the customers a user acts for.
	AdminCustomerServiceKey = "xip.services.admin.customers"
)
