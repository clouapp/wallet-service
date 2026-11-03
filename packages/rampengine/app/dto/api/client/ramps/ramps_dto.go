// Package ramps contains DTOs for the client on-ramp, off-ramp and standing
// virtual account API.
package ramps

// EstimateOnrampDTO prices BRL in against the crypto the user would receive.
type EstimateOnrampDTO struct {
	AmountBRL string
	Token     string
	Chain     string
	// Wallet is the user's own address. XIP never takes custody, so the crypto
	// is delivered straight to it.
	Wallet string
} //	@name	ClientEstimateOnrampDTO

// CreateOnrampDTO places the pay-in order. Unlike the off-ramp's, it carries
// no MFA code: the money moves toward the user's own wallet and the user is
// the one paying, so there is no arbitrary destination for a step-up to
// protect.
type CreateOnrampDTO struct {
	EstimateOnrampDTO
	EstimateID string
} //	@name	ClientCreateOnrampDTO

// EstimateOfframpDTO prices crypto in against the BRL the user would receive.
type EstimateOfframpDTO struct {
	AmountToken string
	Token       string
	Chain       string
	// FromAddress is the user's own wallet the crypto leaves.
	FromAddress string
	PixKey      string
	PixKeyType  string
	// RecipientName is who the PIX key belongs to. Bridge registers a payout
	// destination as its own object and requires it; only the caller knows it,
	// since a CPF key is just a number.
	RecipientName string
	// RecipientDocument is the CPF or CNPJ (digits, and letters for an
	// alphanumeric CNPJ) for phone, email, or evp keys.
	RecipientDocument string
	// Rail is the payout rail asked for: empty or pix_payout for a Pix key,
	// ach_payout or wire_payout for a registered US bank account. The service
	// decides which quote it is from this field.
	Rail string
	// BankBeneficiaryID is the external id of the customer's bank beneficiary
	// a bank rail pays out to. Empty on Pix.
	BankBeneficiaryID string
	// FundingFormats is what the app can do with a payout's funding: "deposit"
	// (the default; send crypto to the address the provider gives) and
	// "prepared" (sign the transaction the provider prepared). A client that
	// does not declare "prepared" is never quoted by a provider that needs it.
	FundingFormats []string
} //	@name	ClientEstimateOfframpDTO

// CreateOfframpDTO places the payout order. Code/CodeType carry the MFA
// step-up.
type CreateOfframpDTO struct {
	EstimateOfframpDTO
	EstimateID string
	Code       string
	CodeType   string
} //	@name	ClientCreateOfframpDTO

// ProvisionVirtualAccountDTO is where a standing account delivers to. There
// is no amount: the deposit code is permanent and carries none.
type ProvisionVirtualAccountDTO struct {
	// SourceCurrency is the fiat the account collects, lowercase.
	SourceCurrency string
	Token          string
	Chain          string
	// Wallet is the user's own address. XIP never takes custody, so every
	// deposit is converted straight into it.
	Wallet string
} //	@name	ClientProvisionVirtualAccountDTO

// UpdateVirtualAccountDTO changes where an existing account delivers.
//
// The destination fields are the same a provision states — the whole
// destination, and a token without its chain names no address. Code/CodeType
// carry the MFA step-up: this path redirects every future deposit while the
// deposit code stays the same, so a stolen session must not be enough.
type UpdateVirtualAccountDTO struct {
	ProvisionVirtualAccountDTO
	Code     string
	CodeType string
} //	@name	ClientUpdateVirtualAccountDTO

// RegisterPixAccountDTO is the Pix destination a payout registers at a
// provider that holds it as an object of its own. It is service input only: no
// request body carries it.
type RegisterPixAccountDTO struct {
	Provider          string
	PixKey            string
	PixKeyType        string
	RecipientName     string
	RecipientDocument string
} //	@name	ClientRegisterPixAccountDTO
