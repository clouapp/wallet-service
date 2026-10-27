package commands

import (
	"context"
	"strings"
	"testing"

	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/settings"
)

type fakeImporter struct {
	actor string
	apply bool
	calls int
	err   error
}

func (f *fakeImporter) ImportPlatformEnvAs(_ context.Context, actorEmail string, lookup func(string) (string, bool), apply, _ bool) ([]settings.EnvImportGroup, error) {
	f.actor, f.apply = actorEmail, apply
	f.calls++
	_, _ = lookup("TRON_API_KEY")
	if f.err != nil {
		return nil, f.err
	}
	return []settings.EnvImportGroup{{Group: settings.GroupProviderTron, Written: apply, Fields: []settings.EnvImportField{
		{Key: "api_key", Env: "TRON_API_KEY", Secret: true},
	}}}, nil
}

func TestSettings_ImportEnv_DryRunPrintsNamesNeverValues(t *testing.T) {
	importer := &fakeImporter{}
	cmd := NewSettingsImportEnv(importer)
	cmd.lookup = func(string) (string, bool) { return "tron-secret-value", true }

	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Option("actor").Return("admin@example.test")
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().OptionBool("overwrite").Return(false)
	ctx.EXPECT().Info(mock.AnythingOfType("string")).Return()
	ctx.EXPECT().Line(mock.MatchedBy(func(line string) bool {
		return strings.Contains(line, "TRON_API_KEY") && !strings.Contains(line, "tron-secret-value")
	})).Return()

	if err := cmd.Handle(ctx); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if importer.calls != 1 || importer.actor != "admin@example.test" || importer.apply {
		t.Fatalf("importer called %d times as %q apply %v", importer.calls, importer.actor, importer.apply)
	}
}

func TestSettings_ImportEnv_RequiresAKnownActor(t *testing.T) {
	importer := &fakeImporter{}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Option("actor").Return("")
	ctx.EXPECT().Error(mock.AnythingOfType("string")).Return()
	if err := NewSettingsImportEnv(importer).Handle(ctx); err == nil {
		t.Fatal("a run without --actor must fail")
	}
	if importer.calls != 0 {
		t.Fatal("nothing may be imported without an actor")
	}

	unknown := &fakeImporter{err: settings.ErrImportActorUnknown}
	failing := mocksconsole.NewContext(t)
	failing.EXPECT().Option("actor").Return("ghost@example.test")
	failing.EXPECT().OptionBool("apply").Return(true)
	failing.EXPECT().OptionBool("overwrite").Return(false)
	failing.EXPECT().Error(mock.AnythingOfType("string")).Return()
	if err := NewSettingsImportEnv(unknown).Handle(failing); err == nil {
		t.Fatal("an unknown actor must fail")
	}
}
