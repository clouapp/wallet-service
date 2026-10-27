package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

// envImporter copies the environment's values into the platform settings
// (*settings.Service).
type envImporter interface {
	ImportPlatformEnv(ctx context.Context, actorID uuid.UUID, lookup func(string) (string, bool), apply, overwrite bool) ([]settings.EnvImportGroup, error)
}

// actorLookup finds the platform admin a save is recorded under.
type actorLookup interface {
	FindByEmail(ctx context.Context, email string) (*models.User, error)
}

// SettingsImportEnv copies, once, the values of the variables a platform setting
// replaces (LOCAL_DEPOSIT_SCAN_CHAINS, LOCAL_XRP_STREAM_*, TRON_API_KEY,
// <PREFIX>_FALLBACK_*, THROTTLE_*, WITHDRAWAL_STALE_AFTER_HOURS, APP_FRONTEND_URL,
// DEPOSIT_SCAN_ALERT_*, DEPOSIT_SIGNATURE_CHAINS) into the platform settings, so
// they can then be removed from the environment. It prints variable names, never
// values, and writes nothing without --apply.
type SettingsImportEnv struct {
	settings envImporter
	users    actorLookup
	lookup   func(string) (string, bool)
}

// NewSettingsImportEnv wires the command over the process environment.
func NewSettingsImportEnv(settingsService envImporter, users actorLookup) *SettingsImportEnv {
	if settingsService == nil || users == nil {
		panic("settings:import-env: settings service and user lookup are required")
	}
	return &SettingsImportEnv{settings: settingsService, users: users, lookup: os.LookupEnv}
}

func (c *SettingsImportEnv) Signature() string { return "settings:import-env" }

func (c *SettingsImportEnv) Description() string {
	return "Copy the environment values the platform settings replace into the settings (names printed, never values); dry run unless --apply"
}

func (c *SettingsImportEnv) Extend() command.Extend {
	return command.Extend{
		Category: "settings",
		Flags: []command.Flag{
			&command.StringFlag{Name: "actor", Usage: "email of the platform admin the saves are recorded under (required)"},
			&command.BoolFlag{Name: "apply", Usage: "write the settings (default: print the plan only)"},
			&command.BoolFlag{Name: "overwrite", Usage: "also replace a field a row already stores"},
		},
	}
}

func (c *SettingsImportEnv) Handle(ctx console.Context) error {
	email := strings.TrimSpace(ctx.Option("actor"))
	if email == "" {
		return fail(ctx, errors.New("--actor=<platform admin email> is required"))
	}
	background := context.Background()
	user, err := c.users.FindByEmail(background, email)
	if err != nil || user == nil || user.ID == uuid.Nil {
		return fail(ctx, errors.New("no user has that email"))
	}
	apply := ctx.OptionBool("apply")
	groups, err := c.settings.ImportPlatformEnv(background, user.ID, c.lookup, apply, ctx.OptionBool("overwrite"))
	for _, group := range groups {
		writes := 0
		for _, field := range group.Fields {
			if field.Skipped == "" {
				writes++
			}
		}
		state := "nothing to copy"
		switch {
		case group.Written:
			state = fmt.Sprintf("written (%d fields)", writes)
		case writes > 0:
			state = fmt.Sprintf("would write %d fields", writes)
		}
		ctx.Info(group.Group + ": " + state)
		for _, field := range group.Fields {
			secret := ""
			if field.Secret {
				secret = " (secret, sealed)"
			}
			if field.Skipped == "" {
				ctx.Line(fmt.Sprintf("  %s <- %s%s", field.Key, field.Env, secret))
				continue
			}
			ctx.Line(fmt.Sprintf("  %s: %s %s", field.Key, field.Env, field.Skipped))
		}
	}
	if err != nil {
		return fail(ctx, err)
	}
	if !apply {
		ctx.Info("dry run: nothing written (pass --apply)")
		return nil
	}
	ctx.Info("settings written; once each one is checked on the panel, the variables can leave the environment")
	return nil
}
