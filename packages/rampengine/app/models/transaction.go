package models

import (
	"slices"
	"time"

	"github.com/goravel/framework/database/orm"
)

const (
	ProviderBridge    = "bridge"
	ProviderMutualPay = "mutualpay"
	ProviderOzav      = "ozav"
	ProviderBlindPay  = "blindpay"

	// ProviderModeSandbox and ProviderModeProduction select which credentials
	// an adapter uses. The mode is validated against APP_ENV before a provider
	// can be enabled, so a production deploy cannot silently run against a
	// sandbox key.
	ProviderModeSandbox    = "sandbox"
	ProviderModeProduction = "production"

	// Rails are the provider-agnostic vocabulary for "which leg of which
	// direction". They key the `rail_<rail>` settings group and are persisted
	// on `transactions.rail`; the ramp package wraps them in a typed `Rail`.
	RailPixPayin  = "pix_payin"
	RailPixPayout = "pix_payout"
	// The USD rails. A deposit is one rail whatever network the sender's bank
	// picks (ACH, wire, FedNow land in the same virtual account); a payout is
	// one rail per network, because fee, timing and what the destination
	// account must carry differ between them.
	RailUSDPayin      = "usd_payin"
	RailUSDACHPayout  = "usd_ach_payout"
	RailUSDWirePayout = "usd_wire_payout"

	// FiatCurrencyBRL and FiatCurrencyUSD are the fiat legs a rail settles in,
	// lowercase as transactions.fiat_currency stores them.
	FiatCurrencyBRL = "brl"
	FiatCurrencyUSD = "usd"

	// The bank payout rails: a stablecoin leaving the user's wallet for a US
	// bank account, by ACH or by domestic wire. They are rails of their own
	// rather than variants of pix_payout because everything keyed by a rail
	// differs: the routing policy, the currency the limits are in, and the
	// switch that closes them.
	RailACHPayout  = "ach_payout"
	RailWirePayout = "wire_payout"

	TransactionTypeOnramp  = "onramp"
	TransactionTypeOfframp = "offramp"

	TargetTokenUSDC = "USDC"
	TargetTokenUSDT = "USDT"
	TargetTokenBRS  = "BRS"
	TargetTokenSOL  = "SOL"

	// ChainSolana is the one network XIP's ramps move tokens on.
	ChainSolana = "solana"

	// Canonical transaction statuses, shared by every provider. Adapters map
	// their own vocabulary onto these before anything is persisted.
	TxStatusPending       = "pending"
	TxStatusAwaitingFunds = "awaiting_funds"
	TxStatusFundsReceived = "funds_received"
	TxStatusProcessing    = "processing"
	TxStatusCompleted     = "completed"
	TxStatusFailed        = "failed"
	TxStatusRefunded      = "refunded"
	TxStatusExpired       = "expired"

	// TxStatusReturning is a payout the beneficiary's bank sent back, now in
	// the provider's refund review. It is not terminal: the money is still on
	// its way back, and the reconciler keeps reading the order until it lands.
	TxStatusReturning = "returning"
	// TxStatusRefundAttention is a payout whose crypto the provider collected
	// and whose fiat it did not deliver, with no automatic refund under way.
	// It is not terminal on purpose: it needs an operator, and closing it
	// would leave a user's money with nobody looking for it.
	TxStatusRefundAttention = "refund_attention"
)

// AttentionBindingMismatch is the attention code of a provider read that did
// not match the payout binding stored at finalize.
const AttentionBindingMismatch = "binding_mismatch"

// PixRails is the closed set of Pix rails, payin before payout: the one list
// of them, which ramp.PixRails types as ramp.Rail. A fresh slice each call,
// so no caller can change the vocabulary for the others.
func PixRails() []string { return []string{RailPixPayin, RailPixPayout} }

// BankRails is the closed set of bank payout rails, ACH before wire: the one
// list of them, which ramp.BankRails types as ramp.Rail. A fresh slice each
// call, for the same reason as PixRails.
func BankRails() []string { return []string{RailACHPayout, RailWirePayout} }

// FundingModePrepared marks an order funded by a wallet-signed action the
// provider prepared (the bank payouts, and a Pix payout the provider serves
// that way). An order without it is funded by a deposit.
const FundingModePrepared = "prepared"

// USDRails is the closed set of USD rails, payin first. A fresh slice each
// call, like PixRails.
func USDRails() []string { return []string{RailUSDPayin, RailUSDACHPayout, RailUSDWirePayout} }

// AllRails is every rail a read model walks when it means "every rail": Pix,
// then USD. The bank payout rails (BankRails) are not in it on purpose: they
// are advertised only where their provider exists, never by a walk of this
// list. PixRails stays for the places that mean Pix alone.
func AllRails() []string { return append(PixRails(), USDRails()...) }

// RailFiatCurrency is the fiat a rail settles in: "brl" for the Pix rails,
// "usd" for the USD and bank ones, "" for a rail the stack does not know.
func RailFiatCurrency(rail string) string {
	switch rail {
	case RailPixPayin, RailPixPayout:
		return FiatCurrencyBRL
	case RailUSDPayin, RailUSDACHPayout, RailUSDWirePayout, RailACHPayout, RailWirePayout:
		return FiatCurrencyUSD
	}
	return ""
}

// The slices below are the complete vocabularies, in the order a report or a
// dashboard should present them. They live beside the constants so adding
// another status is one edit rather than one per consumer.
var (
	// AllTxStatuses lists every canonical transaction status.
	AllTxStatuses = []string{
		TxStatusPending, TxStatusAwaitingFunds, TxStatusFundsReceived,
		TxStatusProcessing, TxStatusReturning, TxStatusRefundAttention,
		TxStatusCompleted, TxStatusFailed, TxStatusRefunded, TxStatusExpired,
	}

	// AllTransactionTypes lists the canonical transaction types. The legacy
	// payin/payout names are folded onto these before they reach a caller.
	AllTransactionTypes = []string{TransactionTypeOnramp, TransactionTypeOfframp}

	// AllTargetTokens lists the tokens XIP settles in.
	AllTargetTokens = []string{TargetTokenUSDC, TargetTokenSOL, TargetTokenBRS}

	// RampTokens are the stablecoins a ramp offers. Each adapter declares the
	// subset it serves, the capabilities read model walks all of them, and the
	// ramp requests accept them (plus SOL, a legitimate ask no provider serves
	// yet).
	RampTokens = []string{TargetTokenUSDC, TargetTokenUSDT, TargetTokenBRS}
)

// Transaction mirrors a payment-gateway order of a customer. The `type`
// column accommodates `payout` in a later spec without renaming; `provider`
// identifies which gateway owns the order so a second gateway can be added
// without schema changes.
type Transaction struct {
	orm.Model
	orm.SoftDeletes
	// CustomerID is whose money the order moves (spec §3.2): a person's own
	// customer, whose id is theirs, or a company's.
	CustomerID uint `gorm:"not null;index" json:"customer_id"`
	// UserID is who acted: the member who placed the order, or, for one a
	// deposit webhook opened, the member who provisioned the account it
	// arrived in (spec §18.6).
	UserID         uint   `gorm:"not null;index" json:"user_id"`
	Provider       string `gorm:"size:32;not null;index" json:"provider"`
	Type           string `gorm:"size:16;not null;index" json:"type"`
	ExternalID     string `gorm:"type:uuid;not null;uniqueIndex" json:"external_id"`
	GatewayOrderID string `gorm:"size:64;not null" json:"gateway_order_id"`
	// GatewayQuoteID is nullable because a non-binding estimate has no upstream
	// quote id. Writing "" instead would make the user's second ramp order
	// collide on the unique that used to cover this column.
	GatewayQuoteID *string `gorm:"size:64" json:"gateway_quote_id,omitempty"`
	Status         string  `gorm:"size:32;not null;index" json:"status"`
	// AmountFiat is the fiat leg, in FiatCurrency. The API still answers
	// `amount_brl` on BRL orders through LegacyAmountBRL, because installed
	// apps read it.
	AmountFiat string `gorm:"type:numeric(20,2);not null" json:"amount_fiat"`
	// FiatCurrency is the currency of AmountFiat, lowercase: "brl" or "usd".
	// The gorm default lets a row built without it (every Pix path) take the
	// column's 'brl' rather than an empty string.
	FiatCurrency string `gorm:"size:8;not null;default:brl" json:"fiat_currency"`
	// AmountToken is the crypto amount: the amount bought (payin) or deposited (payout).
	AmountToken string `gorm:"type:numeric(38,18);not null" json:"amount_token"`
	// TargetToken is the token of the order: Astronpay's `targetToken` for
	// payin and `sourceToken` for payout.
	TargetToken string `gorm:"size:16;not null" json:"target_token"`
	// DestinationWallet is where the crypto goes: the user's wallet for payin,
	// Astronpay's deposit address for payout.
	DestinationWallet string  `gorm:"size:64;not null" json:"destination_wallet"`
	PixPaymentCode    *string `gorm:"type:text" json:"pix_payment_code,omitempty"`
	// Payout-only fields. PaymentReference is the per-order Solana Pay reference
	// key Astronpay uses to correlate the on-chain deposit with the order;
	// DepositTxSignature is the deposit's on-chain signature reported by the
	// order.deposit_received webhook.
	PixKey             *string    `gorm:"type:text" json:"pix_key,omitempty"`
	PixKeyType         *string    `gorm:"size:16" json:"pix_key_type,omitempty"`
	RecipientName      *string    `gorm:"size:255" json:"recipient_name,omitempty"`
	RecipientDocument  *string    `gorm:"size:18" json:"recipient_document,omitempty"`
	PaymentReference   *string    `gorm:"size:64" json:"payment_reference,omitempty"`
	DepositTxSignature *string    `gorm:"size:96" json:"deposit_tx_signature,omitempty"`
	FailureReason      *string    `gorm:"type:text" json:"failure_reason,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	// Ramp fields, all nullable so the Astronpay rows already in the table are
	// untouched. FeeAmount is meaningless without FeeCurrency, the currency
	// the provider charges the fee in: BRL on Bridge's and Ozav's on-ramps,
	// the token on their off-ramps and on BlindPay's partner fee.
	Rail        *string `gorm:"size:24;index" json:"rail,omitempty"`
	FeePercent  *string `gorm:"type:numeric(5,2)" json:"fee_percent,omitempty"`
	FeeAmount   *string `gorm:"type:numeric(38,18)" json:"fee_amount,omitempty"`
	FeeCurrency *string `gorm:"size:16" json:"fee_currency,omitempty"`
	// EstimateID is the internal id of the ramp_estimates row this order was
	// priced from; the client only ever sees that row's external_id.
	EstimateID *uint `gorm:"index" json:"estimate_id,omitempty"`
	// ProviderDestinationRef is a provider-side handle for the destination
	// (e.g. a wallet or external-account id) when the provider needs one.
	ProviderDestinationRef *string `gorm:"size:64" json:"provider_destination_ref,omitempty"`
	// DeliveryOrderID is the second movement an order-first on-ramp needs: the
	// provider bought the crypto into its own custody and still has to send it
	// to the user's wallet. Its presence means that send was already placed,
	// which is what keeps a retry, a duplicate webhook and the reconciler from
	// each paying the user again. Null for a provider that delivers in one hop.
	DeliveryOrderID *string `gorm:"size:64" json:"delivery_order_id,omitempty"`
	// DeliveryAttempts counts failed on-ramp delivery transfers retried by the reconciler.
	DeliveryAttempts int `gorm:"not null;default:0" json:"delivery_attempts"`
	// Chain is the network the crypto leg settles on. Kept on the order because
	// a delivery has to name the network it sends on, and the estimate's
	// snapshot — the only place it used to live — is a pricing row a settlement
	// step should not have to depend on.
	Chain *string `gorm:"size:24" json:"chain,omitempty"`
	// FromAddress is the user's own wallet funding an off-ramp.
	FromAddress *string `gorm:"size:64" json:"from_address,omitempty"`
	// IdempotencyKey is derived from ExternalID and persisted before the
	// upstream create, so a retry sends the provider the same key. The
	// database enforces `(provider, idempotency_key)`.
	IdempotencyKey *string `gorm:"size:64" json:"idempotency_key,omitempty"`
	// DepositMode is how an off-ramp expects to be funded: simulated in sandbox
	// or on_chain in production. Set at create from the provider mode then and
	// frozen on the row so simulate-deposit cannot follow a later mode change.
	DepositMode *string `gorm:"size:16" json:"deposit_mode,omitempty"`
	// DepositInstructions is what the user needs to fund the order (a PIX
	// br_code on-ramp, a deposit address off-ramp), as jsonb-compatible text.
	DepositInstructions *string `gorm:"type:jsonb" json:"-"`
	// Virtual-account fields, all nullable: only the rows a deposit event
	// created carry them.
	//
	// VirtualAccountID is the standing account the money arrived in. It is also
	// what tells the reconciler to re-read the deposit out of the account's
	// history instead of asking for a transfer that does not exist.
	VirtualAccountID *uint `gorm:"index" json:"virtual_account_id,omitempty"`
	// DepositID is the order identity on this rail. There is no client create,
	// so neither IdempotencyKey nor EstimateID applies: the provider's deposit
	// id is the only thing guaranteed unique per payment received, and it is
	// what links the funds event to the payment ones. It is written to
	// GatewayOrderID as well; the dedicated column exists so the partial unique
	// `(provider, deposit_id)` — which is what makes the webhook upsert
	// race-safe — stays specific to this rail.
	DepositID *string `gorm:"size:64" json:"deposit_id,omitempty"`
	// SenderName and SenderReference are who paid and their payment reference.
	// They are the only handle an operator has on a third-party deposit, and
	// they are PII: persisted, never logged.
	SenderName      *string `gorm:"size:255" json:"sender_name,omitempty"`
	SenderReference *string `gorm:"size:255" json:"sender_reference,omitempty"`
	// ExchangeFeeAmount is the provider's conversion fee, distinct from
	// FeeAmount (XIP's cut). Read from the receipt, never computed locally.
	ExchangeFeeAmount *string `gorm:"type:numeric(38,18)" json:"exchange_fee_amount,omitempty"`
	// ExchangeRate is the provider's settled conversion rate, persisted on
	// refresh so the client projection does not parse provider-specific JSON.
	ExchangeRate *string `gorm:"type:numeric(38,18)" json:"exchange_rate,omitempty"`
	// PaymentRail is the bank network that moved the fiat leg: ach, wire or
	// fednow on a USD order, pix on a Pix deposit when the provider says so.
	// Nil until the provider reports it.
	PaymentRail *string `gorm:"size:16" json:"payment_rail,omitempty"`
	// BankAccountID is the user's US bank account a USD payout pays into.
	BankAccountID *uint `json:"bank_account_id,omitempty"`
	// LiquidationAddressID is the standing payout address whose drain created
	// this USD payout; like VirtualAccountID, it is what tells the reconciler
	// to re-read the drain instead of a transfer.
	LiquidationAddressID *uint `gorm:"index" json:"liquidation_address_id,omitempty"`
	// RawPayload is stored as string (not []byte) so pgx encodes it with the
	// text OID; Postgres then implicitly casts the text → jsonb. Sending a
	// raw []byte makes pgx use the bytea OID which Postgres refuses to cast.
	RawPayload string `gorm:"type:jsonb" json:"-"`
	// Bank payout leg, only set on a bank rail: what the bank receives is
	// AmountFiat, in FiatCurrency. BankBeneficiaryID goes null if the
	// beneficiary is purged with the account; the last-four columns keep the
	// history legible.
	BankBeneficiaryID *uint   `gorm:"index" json:"bank_beneficiary_id,omitempty"`
	BankRail          *string `gorm:"size:24" json:"bank_rail,omitempty"`
	BankAccountType   *string `gorm:"size:16" json:"bank_account_type,omitempty"`
	BankAccountLast4  *string `gorm:"size:4" json:"bank_account_last4,omitempty"`
	BankRoutingLast4  *string `gorm:"size:4" json:"bank_routing_last4,omitempty"`
	// FundingMode is FundingModePrepared for an order funded by a prepared
	// action. Bank rails are always funded that way and carry no mode (rows
	// older than the column); read it through FundedByAuthorization.
	FundingMode *string `gorm:"size:16" json:"funding_mode,omitempty"`
	// AttentionCode marks a row the ramp attention queue shows (a binding the
	// provider's order did not match); AttentionAt is when it was first marked.
	// Cleared on the first clean read. The SupportTicket* columns record the
	// operator's chamado with the provider, without moving the status.
	AttentionCode     *string    `gorm:"size:64" json:"-"`
	AttentionAt       *time.Time `json:"-"`
	SupportTicketRef  *string    `gorm:"size:64" json:"-"`
	SupportTicketNote *string    `gorm:"type:text" json:"-"`
	SupportTicketAt   *time.Time `json:"-"`
	// User is the owning end-user, eager-loaded only by the admin listing via
	// .With("User"); nil in the client onramp/offramp flows (they never preload it).
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// IsTerminalStatus reports whether a status string is one of the terminal
// states a transaction can no longer leave.
//
// One vocabulary answers for the whole table: the Astronpay rows were folded
// onto these values by 20260805120700_normalize_transaction_statuses, so there
// is no `COMPLETED` left for this to miss.
func IsTerminalStatus(status string) bool {
	return slices.Contains(TerminalTxStatuses, status)
}

// TerminalTxStatuses lists the terminal statuses, for a query that has to ask
// the same question IsTerminalStatus answers.
var TerminalTxStatuses = []string{TxStatusCompleted, TxStatusFailed, TxStatusExpired, TxStatusRefunded}

// IsTerminal reports whether the transaction is in a terminal state.
func (t *Transaction) IsTerminal() bool {
	return IsTerminalStatus(t.Status)
}

// LegacyAmountBRL is what the API's `amount_brl` carries: the fiat leg of a
// BRL order, and "" on any other currency, so no client reads dollars as reais.
func (t *Transaction) LegacyAmountBRL() string {
	if t.FiatCurrency == "" || t.FiatCurrency == FiatCurrencyBRL {
		return t.AmountFiat
	}
	return ""
}

// AwaitsDelivery reports whether an on-ramp's pay-in settled and its crypto has
// not been sent on yet: the one state in which the second hop is due. It is the
// question every path that can see an on-ramp complete asks — the provider's
// event, a read's refresh, the reconciler — and FindUndeliveredOnramps asks it
// in SQL. A delivery the provider reported failed has its id cleared and its
// attempt counted (rampsvc.ApplyDeliveryOutcome), which makes it due again
// until the attempt ceiling.
func (t *Transaction) AwaitsDelivery() bool {
	return t.Type == TransactionTypeOnramp && t.Status == TxStatusCompleted &&
		(t.DeliveryOrderID == nil || *t.DeliveryOrderID == "")
}

// CanLeaveTerminal reports whether a transaction already in terminal status
// `current` may still move to `next`. A refund is the one path out of a
// completed order. Expiry is a local decision about an unfunded payment
// window; when a provider later reports funds, that overrides the expiry.
// Processing is not such a report: it is also every adapter's fallback for a
// status it cannot name, so it would revive an order nobody funded.
// Anything else (e.g. an out-of-order failure after a completion) must not
// overwrite settled state.
func CanLeaveTerminal(current, next string) bool {
	if current == TxStatusCompleted {
		return next == TxStatusRefunded
	}
	if current != TxStatusExpired {
		return false
	}
	switch next {
	case TxStatusFundsReceived, TxStatusCompleted,
		TxStatusReturning, TxStatusRefundAttention, TxStatusRefunded:
		return true
	default:
		return false
	}
}

// RegressesStatus reports whether moving a payout from `current` to `next`
// walks it back: a payout the bank returned (`returning`) or that waits on an
// operator (`refund_attention`) is past `processing`, and a stale or reordered
// provider read must not pull it there again. Nor may failed or expired: they are
// terminal, leave the attention queue and the sweep, and would hide the funds
// already held. completed and refunded stay allowed. A status it does not know never
// regresses, so a vocabulary this code has not met cannot freeze a row.
func RegressesStatus(current, next string) bool {
	if current != TxStatusReturning && current != TxStatusRefundAttention {
		return false
	}
	switch next {
	case TxStatusPending, TxStatusAwaitingFunds, TxStatusFundsReceived, TxStatusProcessing,
		TxStatusFailed, TxStatusExpired:
		return true
	default:
		return false
	}
}

// FundedByAuthorization reports whether the order is funded by a prepared
// action the wallet signs and the backend finalizes. Bank rails always are
// (rows written before the column carry no mode); a Pix payout is only when
// the provider that quoted it said so.
func (t *Transaction) FundedByAuthorization() bool {
	if t.Rail != nil && slices.Contains(BankRails(), *t.Rail) {
		return true
	}
	return t.FundingMode != nil && *t.FundingMode == FundingModePrepared
}
