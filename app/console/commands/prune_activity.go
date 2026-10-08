package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/activity"
)

// PruneActivity deletes audit rows older than the retention window.
// It prunes activity_log and account_activity. It prints a count, never a row.
type PruneActivity struct {
	activity *activity.Service
}

// NewPruneActivity wires the activity service that deletes the old rows.
func NewPruneActivity(activity *activity.Service) *PruneActivity {
	return &PruneActivity{activity: activity}
}

func (c *PruneActivity) Signature() string { return "activity:prune" }

func (c *PruneActivity) Description() string {
	return "Delete activity rows older than the retention window"
}

func (c *PruneActivity) Extend() command.Extend {
	return command.Extend{
		Category: "activity",
		Flags: []command.Flag{
			&command.StringFlag{Name: "days", Usage: "retention window in days (default 365)"},
		},
	}
}

func (c *PruneActivity) Handle(ctx console.Context) error {
	line, err := c.activity.Prune(context.Background(), ctx.Option("days"))
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(line)
	return nil
}
