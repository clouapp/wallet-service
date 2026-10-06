package activity

import (
	"context"
	"fmt"
	"strconv"
)

const (
	defaultRetentionDays = 365
	pruneBatch           = 1000
)

// Prune deletes activity_log and account_activity rows older than daysFlag
// (empty means 365). It returns the operator line and never a row.
func (s *Service) Prune(ctx context.Context, daysFlag string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("prune activity: context is required")
	}
	if s == nil || s.activityLog == nil || s.accountActivity == nil {
		return "", fmt.Errorf("prune activity: stores are required")
	}
	days, err := retentionDays(daysFlag)
	if err != nil {
		return "", err
	}
	logRows, err := pruneBatches(ctx, s.activityLog, "activity_log", days)
	if err != nil {
		return "", err
	}
	accountRows, err := pruneBatches(ctx, s.accountActivity, "account_activity", days)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pruned %d activity_log rows and %d account_activity rows older than %d days", logRows, accountRows, days), nil
}

func retentionDays(flag string) (int, error) {
	if flag == "" {
		return defaultRetentionDays, nil
	}
	days, err := strconv.Atoi(flag)
	if err != nil || days <= 0 {
		return 0, fmt.Errorf("invalid retention window %q: expected a positive number of days", flag)
	}
	return days, nil
}

func pruneBatches(ctx context.Context, store BatchDeleter, table string, days int) (int64, error) {
	var total int64
	for {
		n, err := store.DeleteOlderThan(ctx, days, pruneBatch)
		if err != nil {
			return total, fmt.Errorf("prune %s failed after %d rows: %w", table, total, err)
		}
		total += n
		if n < int64(pruneBatch) {
			return total, nil
		}
	}
}
