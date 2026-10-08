package migrations

import (
	"github.com/goravel/framework/contracts/crypt"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"
)

// The 420-560 migrations reach the database and the cipher through these three
// functions, so the framework facades are imported here and nowhere else in that
// range.

func migrationQuery() orm.Query {
	return facades.Orm().Query()
}

func migrationTransaction(callback func(tx orm.Query) error) error {
	return facades.Orm().Transaction(callback)
}

func migrationCipher() crypt.Crypt {
	return facades.Crypt()
}
