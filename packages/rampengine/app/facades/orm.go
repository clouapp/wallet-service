package facades

import (
	"github.com/goravel/framework/contracts/database/orm"
)

func Orm() orm.Orm {
	// A separate schema keeps the ported fiat ledger and contact records out
	// of Macro's authentication, wallets and on-chain transaction tables.
	return App().MakeOrm().Connection("ramps")
}
