package ramps

import (
	"math/big"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
)

// bindOfframpCreate refuses a create whose body diverges from the quote the
// user was shown. Without this the client could estimate one amount and PIX key
// and create with another.
func bindOfframpCreate(in dto.CreateOfframpDTO, snap *estimateSnapshot) error {
	if !amountsEqual(in.AmountToken, snap.SourceAmount) ||
		in.PixKey != snap.PixKey || in.PixKeyType != snap.PixKeyType ||
		in.Token != snap.SourceCurrency || in.FromAddress != snap.FromAddress ||
		in.Chain != snap.Chain || in.RecipientName != snap.RecipientName {
		return ramp.ErrEstimateMismatch
	}
	return bindRecipientDocument(in.PixKeyType, in.RecipientDocument, snap.RecipientDocument)
}

// bindBankCreate refuses a bank create whose body diverges from the quote:
// the beneficiary, the wallet, the token, the chain and the amount (as a
// number) the user was shown (plan D7). The beneficiary's current revision is
// the caller's to compare, since it is read, not sent.
func bindBankCreate(in dto.CreateOfframpDTO, snap *estimateSnapshot) error {
	if in.BankBeneficiaryID != snap.BankBeneficiaryID || in.FromAddress != snap.FromAddress ||
		in.Token != snap.SourceCurrency || in.Chain != snap.Chain ||
		!amountsEqual(in.AmountToken, snap.SourceAmount) {
		return ramp.ErrEstimateMismatch
	}
	return nil
}

func bindRecipientDocument(pixKeyType, inDoc, snapDoc string) error {
	switch pixKeyType {
	case "phone", "email", "evp":
		if inDoc != snapDoc {
			return ramp.ErrEstimateMismatch
		}
	case "cpf", "cnpj":
		if inDoc != "" && snapDoc != "" && inDoc != snapDoc {
			return ramp.ErrEstimateMismatch
		}
	}
	return nil
}

func amountsEqual(a, b string) bool {
	ra, okA := new(big.Rat).SetString(a)
	rb, okB := new(big.Rat).SetString(b)
	if !okA || !okB {
		return a == b
	}
	return ra.Cmp(rb) == 0
}
