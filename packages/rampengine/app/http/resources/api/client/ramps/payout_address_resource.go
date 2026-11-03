package ramps

import (
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// PayoutAddressResource is the user's standing USD payout address: where they
// send the token that becomes a payout to one of their bank accounts. Like the
// virtual account it never names the provider or its ids.
type PayoutAddressResource struct {
	Rail          string `json:"rail"`
	BankAccountID uint   `json:"bank_account_id"`
	Token         string `json:"token"`
	Chain         string `json:"chain"`
	Address       string `json:"address"`
	Reference     string `json:"reference"`
	FeePercent    string `json:"fee_percent"`
	// MinAmount is in MinAmountCurrency, after fee_percent: Bridge keeps a
	// smaller deposit as dust. The backend refuses below it when the app sends
	// the amount with this call, right before the transfer.
	MinAmount         string `json:"min_amount"`
	MinAmountCurrency string `json:"min_amount_currency"`
	DepositMode       string `json:"deposit_mode"`
	Status            string `json:"status"`
} //	@name	PayoutAddressResource

// NewPayoutAddressResource projects the address.
func NewPayoutAddressResource(view *clientramps.PayoutAddressView) PayoutAddressResource {
	return PayoutAddressResource{
		Rail:              view.Rail,
		BankAccountID:     view.BankAccountID,
		Token:             view.Token,
		Chain:             view.Chain,
		Address:           view.Address,
		Reference:         view.Reference,
		FeePercent:        view.FeePercent,
		MinAmount:         view.MinAmount,
		MinAmountCurrency: models.FiatCurrencyUSD,
		DepositMode:       view.DepositMode,
		Status:            view.Status,
	}
}
