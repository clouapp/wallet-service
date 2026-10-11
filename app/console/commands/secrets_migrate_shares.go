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
	sourceFor func(endpoint string) (sharemigration.Source, error)
}

// SecretsMigrateSharesDeps wires the command. SourceFor builds the Secrets Manager
// the shares are read from; an empty endpoint is AWS itself.
type SecretsMigrateSharesDeps struct {
	Migration *sharemigration.Service
	SourceFor func(endpoint string) (sharemigration.Source, error)
}

// NewSecretsMigrateShares builds the command from deps.
func NewSecretsMigrateShares(deps SecretsMigrateSharesDeps) *SecretsMigrateShares {
	return &SecretsMigrateShares{migration: deps.Migration, sourceFor: deps.SourceFor}
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
			&command.BoolFlag{Name: "apply", Usage: "create the copies and move the ARNs (default: report only)"},
		},
	}
}

func (c *SecretsMigrateShares) Handle(ctx console.Context) error {
	source, err := c.sourceFor(ctx.Option("from-endpoint"))
	if err != nil {
		return fail(ctx, err)
	}
	apply := ctx.OptionBool("apply")
	rows, err := c.migration.Run(context.Background(), source, apply)
	for _, row := range rows {
		to := row.To
		if to == "" {
			to = "-"
		}
		ctx.Info(fmt.Sprintf("%-13s %s %-9s %s -> %s", row.Status, row.WalletID, row.Chain, row.From, to))
	}
	if !apply {
		ctx.Info("dry run: nothing was written; run again with --apply")
	}
	return fail(ctx, err)
}
