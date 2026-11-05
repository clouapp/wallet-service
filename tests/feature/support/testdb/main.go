// Command testdb prepares and cleans the PostgreSQL databases of the parallel
// integration tests (make test-integration):
//
//	go run ./tests/feature/support/testdb prepare      # migrate the template (TEST_DB_DATABASE) fresh
//	go run ./tests/feature/support/testdb drop-clones  # drop its worker clones <template>_pN
//
// Both refuse anything but a database starting with vault_unit_test (never vault or
// vault_test), through the same guard as the test setup.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const usage = "usage: testdb prepare|drop-clones"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "prepare":
		err = prepare()
	case "drop-clones":
		err = dropClones()
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testdb:", err)
		os.Exit(1)
	}
}

func templateName() string {
	if name := strings.TrimSpace(os.Getenv(testenv.DatabaseOverrideVariable)); name != "" {
		return name
	}
	return testenv.DefaultTestDatabaseName
}

func prepare() error {
	// The template itself is migrated, never a clone of it.
	os.Unsetenv(testenv.TemplateVariable)
	testutil.BootTest()
	database := facades.Config().GetString("database.connections.postgres.database")
	if err := testenv.ValidateTemplate(database); err != nil {
		return err
	}
	if database != templateName() {
		return fmt.Errorf("booted on %q, expected the template %q", database, templateName())
	}
	// migrate:fresh only empties public. The fiat engine's schema would survive
	// it, and the migration that installs it would then fail on its own tables.
	if _, err := facades.Orm().Query().Exec("DROP SCHEMA IF EXISTS macro_ramps CASCADE"); err != nil {
		return fmt.Errorf("drop the engine schema of %s: %w", database, err)
	}
	if err := facades.Artisan().Call("migrate:fresh"); err != nil {
		return fmt.Errorf("migrate %s fresh: %w", database, err)
	}
	// The command reports success when a migration fails, leaving a template
	// every clone inherits. Only the record of the last migration proves it ran.
	applied, err := facades.Orm().Query().Table("migrations").Count()
	if err != nil {
		return fmt.Errorf("read the migrations of %s: %w", database, err)
	}
	if want := int64(len(bootstrap.Migrations())); applied != want {
		return fmt.Errorf("template %s applied %d of %d migrations", database, applied, want)
	}
	fmt.Printf("template %s migrated fresh\n", database)
	return nil
}

func dropClones() error {
	os.Unsetenv(testenv.TemplateVariable)
	if err := testenv.Load(); err != nil {
		return err
	}
	dropped, err := testenv.DropWorkerDatabases(templateName())
	for _, name := range dropped {
		fmt.Printf("dropped %s\n", name)
	}
	return err
}
