package chaincatalog

import "github.com/goravel/framework/facades"

func testCatalog() *Catalog {
	return New(facades.Config(), facades.Crypt())
}
