package facades

import (
	"github.com/goravel/framework/contracts/database/schema"
	goravelfacades "github.com/goravel/framework/facades"
)

// Schema returns the schema builder new migrations use.
func Schema() schema.Schema {
	return goravelfacades.Schema()
}
