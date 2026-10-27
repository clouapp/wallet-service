package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

type fakeImporter struct {
	actor     uuid.UUID
	apply     bool
	overwrite bool
	calls     int
}

func (f *fakeImporter) ImportPlatformEnv(_ context.Context, actorID uuid.UUID, lookup func(string) (string, bool), apply, overwrite bool) ([]settings.EnvImportGroup, error) {
	f.actor, f.apply, f.overwrite = actorID, apply, overwrite
	f.calls++
	value, _ := lookup("TRON_API_KEY")
	_ = value
	return []settings.EnvImportGroup{{Group: settings.GroupProviderTron, Written: apply, Fields: []settings.EnvImportField{
		{Key: "api_key", Env: "TRON_API_KEY", Secret: true},
	}}}, nil
}

type fakeUsers struct{ id uuid.UUID }

func (f fakeUsers) FindByEmail(context.Context, string) (*models.User, error) {
	if f.id == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &models.User{ID: f.id}, nil
}

func TestSettings_ImportEnv_DryRunPrintsNamesNeverValues(t *testing.T) {
	importer := &fakeImporter{}
	actor := uuid.New()
	cmd := NewSettingsImportEnv(importer, fakeUsers{id: actor})
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
	if importer.calls != 1 || importer.actor != actor || importer.apply {
		t.Fatalf("importer called %d times as %s apply %v", importer.calls, importer.actor, importer.apply)
	}
}

func TestSettings_ImportEnv_RequiresAKnownActor(t *testing.T) {
	importer := &fakeImporter{}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Option("actor").Return("")
	ctx.EXPECT().Error(mock.AnythingOfType("string")).Return()
	if err := NewSettingsImportEnv(importer, fakeUsers{}).Handle(ctx); err == nil {
		t.Fatal("a run without --actor must fail")
	}

	unknown := mocksconsole.NewContext(t)
	unknown.EXPECT().Option("actor").Return("ghost@example.test")
	unknown.EXPECT().Error(mock.AnythingOfType("string")).Return()
	if err := NewSettingsImportEnv(importer, fakeUsers{}).Handle(unknown); err == nil {
		t.Fatal("an unknown actor must fail")
	}
	if importer.calls != 0 {
		t.Fatal("nothing may be imported without an actor")
	}
}
