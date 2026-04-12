package bootstrap

import (
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/rules"
)

func Rules() []validation.Rule {
	return []validation.Rule{
		&rules.RFC3339{},
		&rules.IntegerString{},
		&rules.DecimalString{},
		&rules.BlockchainAddress{},
		&rules.Unique{},
		&rules.DBExists{},
	}
}
