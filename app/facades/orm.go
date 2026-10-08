package facades

import (
	"github.com/goravel/framework/contracts/database/orm"
	goravelfacades "github.com/goravel/framework/facades"
)

// Orm returns the database ORM. Callers keep the same query API.
func Orm() orm.Orm {
	return goravelfacades.Orm()
}
