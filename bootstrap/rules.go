package bootstrap

import (
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/rules"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

func Rules() []validation.Rule {
	rows := repositories.NewExistence(nil)
	return []validation.Rule{
		&rules.RFC3339{},
		&rules.DecimalString{},
		rules.NewBlockchainAddress(container.MustMake[*chainpkg.Registry]()),
		rules.NewUnique(rows),
		rules.NewDBExists(rows),
	}
}
