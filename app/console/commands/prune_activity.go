package commands

import (
	"fmt"
	"strconv"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/facades"
)

const (
	defaultActivityRetentionDays = 365
	activityPruneBatch           = 1000
)

// PruneActivity deletes audit rows older than the retention window.
// It prunes activity_log and account_activity. It prints a count, never a row.
type PruneActivity struct{}

func NewPruneActivity() *PruneActivity { return &PruneActivity{} }

func (c *PruneActivity) Signature() string { return "activity:prune" }

func (c *PruneActivity) Description() string {
	return "Delete activity rows older than the retention window"
}

func (c *PruneActivity) Extend() command.Extend {
	return command.Extend{
		Category: "activity",
		Flags: []command.Flag{
			&command.StringFlag{
				Name:  "days",
				Usage: "retention window in days (default 365)",
			},
		},
	}
}

func (c *PruneActivity) Handle(ctx console.Context) error {
	days, err := activityRetentionDays(ctx.Option("days"))
	if err != nil {
		ctx.Error(err.Error())
		return err
	}

	logRows, err := pruneTable(ctx, "activity_log", days)
	if err != nil {
		return err
	}
	accountRows, err := pruneTable(ctx, "account_activity", days)
	if err != nil {
		return err
	}
	ctx.Info(fmt.Sprintf("pruned %d activity_log rows and %d account_activity rows older than %d days", logRows, accountRows, days))
	return nil
}

func activityRetentionDays(flag string) (int, error) {
	if flag == "" {
		return defaultActivityRetentionDays, nil
	}
	days, err := strconv.Atoi(flag)
	if err != nil || days <= 0 {
		return 0, fmt.Errorf("invalid retention window %q: expected a positive number of days", flag)
	}
	return days, nil
}

func pruneTable(ctx console.Context, table string, days int) (int64, error) {
	switch table {
	case "activity_log", "account_activity":
	default:
		return 0, fmt.Errorf("prune activity: table %q is not allowed", table)
	}
	var total int64
	for {
		result, err := facades.Orm().Query().Exec(
			`DELETE FROM `+table+` WHERE id IN (
				SELECT id FROM `+table+` WHERE created_at < NOW() - (? * INTERVAL '1 day')
				ORDER BY created_at LIMIT ?
			)`,
			days, activityPruneBatch,
		)
		if err != nil {
			ctx.Error(fmt.Sprintf("prune %s failed after %d rows", table, total))
			return total, fmt.Errorf("prune %s: %w", table, err)
		}
		n := result.RowsAffected
		total += n
		if n < int64(activityPruneBatch) {
			return total, nil
		}
	}
}
