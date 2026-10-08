package snapshot

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Per-secret restore outcomes; "failed:<AWS code>" is built at runtime.
const (
	OutcomeRestored        = "restored"
	OutcomeRestoredNewARN  = "restored-new-arn"
	OutcomePresent         = "present"
	OutcomePresentOtherARN = "present-other-arn"
	OutcomeDifferent       = "different"
	OutcomeMissing         = "missing"
	outcomeFailedPrefix    = "failed:"
)

// Restore recreates secrets missing from LocalStack from the newest readable
// snapshot. It never overwrites a live secret and keeps the original ARN suffix.
// The restore marker (which unlocks Export) is written only when nothing failed.
func (t *Tool) Restore(ctx context.Context) error {
	if err := RequireSeedKey(t.cfg.KeyFile); err != nil {
		return err
	}
	if err := os.Remove(t.cfg.RestoredMarker); err != nil && !os.IsNotExist(err) {
		return safeErrorf("remove restore marker: %v", err)
	}
	if err := os.MkdirAll(t.cfg.Dir, privateDirMode); err != nil {
		return safeErrorf("create %s: %v", t.cfg.Dir, err)
	}
	found, err := t.newestReadable(ctx)
	if err != nil {
		return err
	}
	if found == nil {
		t.log.Printf("no snapshot yet; nothing to restore")
		return t.markRestored()
	}
	outcomes := make(map[string][]string)
	for _, entry := range found.Secrets {
		outcome, err := t.restoreEntry(ctx, entry)
		if err != nil {
			return err
		}
		outcomes[outcome] = append(outcomes[outcome], entry.Name)
	}
	t.logOutcomes(outcomes, found, "restore from")
	if hasFailure(outcomes) {
		return safeErrorf("restore from %s left %s", found.Name, failureSummary(outcomes))
	}
	return t.markRestored()
}

// restoreEntry returns the outcome for one secret; AWS API errors become a
// "failed:<code>" outcome, transport errors abort the whole restore.
func (t *Tool) restoreEntry(ctx context.Context, entry Entry) (string, error) {
	live, err := t.secrets.ReadSecret(ctx, entry.Name)
	if err != nil {
		if !isNotFound(err) {
			return apiFailure(err)
		}
		arn, err := t.secrets.CreateWithOriginalARN(ctx, entry)
		if err != nil {
			return apiFailure(err)
		}
		if arn == entry.ARN {
			return OutcomeRestored, nil
		}
		return OutcomeRestoredNewARN, nil
	}
	return compareLive(live, entry), nil
}

func compareLive(live, entry Entry) string {
	if !live.SameMaterial(entry) {
		return OutcomeDifferent
	}
	if live.ARN == entry.ARN {
		return OutcomePresent
	}
	return OutcomePresentOtherARN
}

func apiFailure(err error) (string, error) {
	code := awsErrorCode(err)
	if code == "" {
		return "", err
	}
	return outcomeFailedPrefix + code, nil
}

func hasFailure(outcomes map[string][]string) bool {
	for outcome := range outcomes {
		if strings.HasPrefix(outcome, outcomeFailedPrefix) {
			return true
		}
	}
	return false
}

func failureSummary(outcomes map[string][]string) string {
	var parts []string
	for _, outcome := range sortedOutcomes(outcomes) {
		if strings.HasPrefix(outcome, outcomeFailedPrefix) {
			parts = append(parts, fmt.Sprintf("%s=%d", outcome, len(outcomes[outcome])))
		}
	}
	return strings.Join(parts, ", ")
}

func sortedOutcomes(outcomes map[string][]string) []string {
	keys := make([]string, 0, len(outcomes))
	for outcome := range outcomes {
		keys = append(keys, outcome)
	}
	sort.Strings(keys)
	return keys
}

// logOutcomes logs every secret that was not simply restored or present, then a
// one-line summary, exactly as the Python tool did.
func (t *Tool) logOutcomes(outcomes map[string][]string, found *loadedSnapshot, verb string) {
	summary := make([]string, 0, len(outcomes))
	for _, outcome := range sortedOutcomes(outcomes) {
		names := outcomes[outcome]
		summary = append(summary, fmt.Sprintf("%s=%d", outcome, len(names)))
		if outcome == OutcomeRestored || outcome == OutcomePresent {
			continue
		}
		for _, name := range names {
			if outcome == OutcomeDifferent {
				t.log.Printf("%s: %s (live value kept)", outcome, name)
				continue
			}
			t.log.Printf("%s: %s", outcome, name)
		}
	}
	t.log.Printf("%s %s: %d in snapshot; %s", verb, found.Name, len(found.Secrets), strings.Join(summary, ", "))
}
