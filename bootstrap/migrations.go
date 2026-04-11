package bootstrap

import (
	"github.com/goravel/framework/contracts/database/schema"

	"github.com/macrowallets/waas/database/migrations"
)

func Migrations() []schema.Migration {
	return migrations.All()
}
