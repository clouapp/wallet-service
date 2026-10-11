package commands

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/sharemigration"
)

// SecretsMigrateShares moves every wallet's share B into the Secrets Manager the
// app uses (sealed with MASTER_KEY_REF) from the one at --from-endpoint, and points
// the wallet rows at the copies. Without --apply it only reports. It prints ARNs,
// never a share.
type SecretsMigrateShares struct {
	migration *sharemigration.Service
}

// NewSecretsMigrateShares wires the command to the migration service.
func NewSecretsMigrateShares(migration *sharemigration.Service) *SecretsMigrateShares {
	return &SecretsMigrateShares{migration: migration}
}

func (c *SecretsMigrateShares) Signature() string { return "secrets:migrate-shares" }

func (c *SecretsMigrateShares) Description() string {
	return "Copy every wallet's share B into this app's Secrets Manager and point the wallets at the copies"
}

func (c *SecretsMigrateShares) Extend() command.Extend {
	return command.Extend{
		Category: "secrets",
		Flags: []command.Flag{
			&command.StringFlag{Name: "from-endpoint", Usage: "Secrets Manager the shares are read from (e.g. http://127.0.0.1:4566 for LocalStack); empty is AWS"},
			&command.StringFlag{Name: "from-profile", Usage: "AWS profile that reads the source (default: the app's own credentials)"},
			&command.BoolFlag{Name: "apply", Usage: "create the copies and move the ARNs (default: report only)"},
		},
	}
}

func (c *SecretsMigrateShares) Handle(ctx console.Context) error {
	apply := ctx.OptionBool("apply")
	report, err := c.migration.Run(context.Background(), sharemigration.Request{
		SourceEndpoint: ctx.Option("from-endpoint"),
		SourceProfile:  ctx.Option("from-profile"),
		Apply:          apply,
	})
	ctx.Info(fmt.Sprintf("target: %s, sealed with %s", orDefault(report.TargetEndpoint, "AWS"), orDefault(report.TargetKMSKeyID, "the default Secrets Manager key")))
	for _, row := range report.Rows {
		ctx.Info(fmt.Sprintf("%-13s %s %-9s %s -> %s", row.Status, row.WalletID, row.Chain, row.From, orDefault(row.To, "-")))
	}
	if !apply {
		ctx.Info("dry run: nothing was written; run again with --apply")
	}
	return fail(ctx, err)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
