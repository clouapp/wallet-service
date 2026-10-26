package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"
)

func TestChains_SeedCatalog_SignatureAndCategory(t *testing.T) {
	cmd := NewChainsSeedCatalog(func(context.Context) error { return nil })
	ext := cmd.Extend()
	if cmd.Signature() != "chains:seed-catalog" || ext.Category != "chains" || len(ext.Arguments) != 0 || len(ext.Flags) != 1 {
		t.Fatalf("signature %q category %q arguments %d flags %d", cmd.Signature(), ext.Category, len(ext.Arguments), len(ext.Flags))
	}
}

func TestChains_SeedCatalog_ConstructorRequiresTheSeeder(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("constructor accepted a missing seeder")
		}
	}()
	NewChainsSeedCatalog(nil)
}

func TestChains_SeedCatalog_DryRunPrintsTheStepsAndWritesNothing(t *testing.T) {
	calls := 0
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Warning(mock.MatchedBy(func(line string) bool {
		return strings.Contains(line, "re-sealed from the environment variable") && strings.Contains(line, "before chains:rpc-endpoints")
	})).Return()
	for range catalogSeedSteps {
		ctx.EXPECT().Info(mock.AnythingOfType("string")).Return().Once()
	}
	ctx.EXPECT().Info("dry run: nothing written (pass --apply)").Return().Once()

	err := NewChainsSeedCatalog(func(context.Context) error { calls++; return nil }).Handle(ctx)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 0 {
		t.Fatalf("seeder called %d times on a dry run", calls)
	}
}

func TestChains_SeedCatalog_ApplySeedsOnceAndNamesTheNextStep(t *testing.T) {
	calls := 0
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Warning(mock.AnythingOfType("string")).Return()
	ctx.EXPECT().Info(mock.AnythingOfType("string")).Return()

	err := NewChainsSeedCatalog(func(context.Context) error { calls++; return nil }).Handle(ctx)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("seeder called %d times, want 1", calls)
	}
	var next bool
	for _, call := range ctx.Calls {
		if call.Method == "Info" && strings.Contains(call.Arguments.String(0), "chains:add-missing --apply") {
			next = true
		}
	}
	if !next {
		t.Fatal("the next step was not printed after seeding")
	}
}

func TestChains_SeedCatalog_ApplyReportsASeederFailure(t *testing.T) {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Warning(mock.AnythingOfType("string")).Return()
	ctx.EXPECT().Info(mock.AnythingOfType("string")).Return()
	ctx.EXPECT().Error(mock.MatchedBy(func(line string) bool { return strings.Contains(line, "chains table is locked") })).Return()

	err := NewChainsSeedCatalog(func(context.Context) error { return errors.New("chains table is locked") }).Handle(ctx)
	if err == nil || !strings.Contains(err.Error(), "chains table is locked") {
		t.Fatalf("err = %v, want the seeder failure", err)
	}
}
