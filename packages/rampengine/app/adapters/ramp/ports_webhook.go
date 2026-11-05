package ramp

import (
	"encoding/json"
	"time"
)

// WebhookKind tells the caller which store a parsed event updates.
type WebhookKind string

const (
	WebhookKindRamp WebhookKind = "ramp"
	WebhookKindKyc  WebhookKind = "kyc"
	// WebhookKindVirtualAccountDeposit is a deposit's lifecycle event. It is
	// separate from WebhookKindRamp because applyRamp requires the transaction
	// to already exist, and the whole point of this kind is that the first
	// event is what creates it.
	WebhookKindVirtualAccountDeposit WebhookKind = "virtual_account_deposit"
	// WebhookKindVirtualAccount is an account-level change (destination
	// updated, deactivated, reactivated). It updates the account row, never a
	// transaction.
	WebhookKindVirtualAccount WebhookKind = "virtual_account"
	// WebhookKindLiquidationDrain is a drain of a liquidation address: crypto
	// the user sent, on its way to their bank account. Like a deposit, the
	// first event is what creates the transaction.
	WebhookKindLiquidationDrain WebhookKind = "liquidation_drain"
	// WebhookKindExternalAccount is a change to a customer's registered bank
	// account. It carries no KYC state itself, but the provider's requirements
	// can move with it (Bridge clears `external_account` from requirements_due),
	// so it is a cue to re-read the customer. ExternalCustomerID is set.
	WebhookKindExternalAccount WebhookKind = "external_account"
	// WebhookKindManagedWalletDeposit is crypto credited into a provider-managed
	// wallet. Ozav's deposit.credited names the wallet, not an existing transfer,
	// so it is what triggers off-ramp settlement after the user funded.
	WebhookKindManagedWalletDeposit WebhookKind = "managed_wallet_deposit"
	WebhookKindUnknown              WebhookKind = "unknown"
)

// ManagedWalletDeposit is a normalized crypto credit into provider custody.
type ManagedWalletDeposit struct {
	DepositID string
	WalletID  string
	Amount    string
	Currency  string
	Network   string
	TxHash    string
}

// WebhookResult is a verified, normalized provider event. EventID is what
// `webhook_events` deduplicates on together with the provider code.
type WebhookResult struct {
	Kind      WebhookKind
	EventID   string
	EventType string
	// ProviderOrderID is set on ramp events, ExternalCustomerID on KYC events.
	ProviderOrderID    string
	ExternalCustomerID string
	// ProviderQuoteID is the quote a ramp event's transfer was created from.
	// It is how an event finds an order whose create was refused after the
	// provider had already made the transfer, so gateway_order_id was never
	// written (see WebhookTx.FindUnplacedOrderByQuoteForUpdate).
	ProviderQuoteID string
	// Status is canonical: a transaction status for ramp events, a KYC status
	// for KYC events.
	Status        string
	TxHash        string
	FailureReason string
	// DeliveryFailed is true when a two-phase on-ramp's delivery transfer
	// failed or was cancelled while the pay-in already settled.
	DeliveryFailed bool
	// ExchangeRate is the provider's settled rate when the event carries a
	// receipt (transfer events). VA deposits report exchange_fee instead.
	ExchangeRate string
	// SourceAmount and DestinationAmount are the settled legs when present.
	SourceAmount      string
	DestinationAmount string
	// ProviderCustomerID, BankAccountID and SenderWallet are the identity a
	// bank payout's event names. The payload carries no quote, network, token
	// or amount, so these are all the processor has to hold against the
	// stored binding before it applies the event's status.
	ProviderCustomerID string
	BankAccountID      string
	SenderWallet       string
	// ExchangeFee is the provider's own conversion cut on a transfer, in the
	// source currency, when the receipt names it.
	ExchangeFee string
	// Endorsements is the KYC snapshot carried by customer events, when the
	// provider includes one — it saves a refresh round-trip.
	Endorsements []Endorsement
	// Requirements and RejectionReasons are what the customer still owes and
	// why they were refused, as the event reports them. They travel for the
	// same reason Endorsements does — the event already carries the snapshot —
	// and they matter more: the client's wizard is derived from Requirements,
	// so an event that moves them and does not persist them leaves the app
	// asking for whatever the last poll happened to see.
	Requirements     []string
	RejectionReasons []string
	// RejectionCategories and RejectionDetails are why the provider refused,
	// in XIP's vocabulary and in its own words (see KycState). They are part
	// of the posture Status states, so they are written whenever Status is
	// set and left alone when it is not.
	RejectionCategories []string
	RejectionDetails    []string
	// ReportsRequirements says the decoder read those two fields off the
	// payload, so an empty slice means "owes nothing" rather than "was never
	// asked". Not every provider puts them in its events — Ozav's carry a
	// status and an endorsement and nothing else — and a decoder that stays
	// silent must leave the row's columns as the last sync wrote them.
	ReportsRequirements bool
	// IntegrationDisabled is a KYC event saying the provider deleted the
	// customer: the link is switched off (IntegrationStatusDisabled), so
	// nothing routes through it, and the row is left as the last sync wrote
	// it. Status and Raw are empty beside it.
	IntegrationDisabled bool
	OccurredAt          *time.Time
	// Deposit is set on WebhookKindVirtualAccountDeposit and carries everything
	// needed to create the transaction the event may be the first news of.
	Deposit *VirtualAccountDeposit
	// Account is set on WebhookKindVirtualAccount.
	Account *VirtualAccount
	// Drain is set on WebhookKindLiquidationDrain.
	Drain *LiquidationDrain
	// ManagedWalletDeposit is set on WebhookKindManagedWalletDeposit.
	ManagedWalletDeposit *ManagedWalletDeposit
	// ProviderAccountID is the virtual account both kinds refer to. It is the
	// join back to `customer_virtual_accounts`, and therefore to the customer: a
	// deposit event names the account, not the order. On a drain it is the
	// liquidation address, the join back to `user_liquidation_addresses`.
	ProviderAccountID string
	// Raw is the untouched payload. It carries PII and is never logged.
	Raw json.RawMessage
}
