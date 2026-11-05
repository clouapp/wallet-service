package bootstrap

import (
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/rules"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	ramprules "github.com/macrowallets/waas/packages/rampengine/app/rules"
)

func Rules() []validation.Rule {
	rows := repositories.NewExistence(nil)
	return []validation.Rule{
		&rules.RFC3339{},
		&ramprules.BridgeSubdivisionRule{},
		&ramprules.BridgePostalCodeRule{},
		&ramprules.KycTaxIdRule{},
		&ramprules.BusinessTaxIdRule{},
		&ramprules.CnpjRule{},
		&ramprules.RecipientDocumentRule{},
		&ramprules.KycCountryRule{},
		&ramprules.KycPhoneRule{},
		&ramprules.KycBirthDateRule{},
		&ramprules.KycNotPoBoxRule{},
		&ramprules.DateStringRule{},
		&ramprules.DateYmdRule{},
		&ramprules.TosAcceptanceRule{},
		&ramprules.KycDocumentFileRule{},
		&ramprules.PixKeyCanonicalRule{},
		&ramprules.PixKeyTypedRule{},
		&ramprules.AbaRoutingRule{},
		&ramprules.UsAccountNumberRule{},
		&ramprules.BankOwnerNameRule{},
		&ramprules.UsStreetLineRule{},
		&ramprules.UsStateRule{},
		&ramprules.UsZipRule{},
		&ramprules.PayoutReferenceRule{},

		&rules.DecimalString{},
		&rules.Uint32{},
		rules.NewBlockchainAddress(container.MustMake[*chainpkg.Registry]()),
		rules.NewUnique(rows),
		rules.NewDBExists(rows),
	}
}
