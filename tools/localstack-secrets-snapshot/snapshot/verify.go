package snapshot

import (
	"context"
)

// Verify is the read-only (dry-run) restore: it decrypts snapshots, checks each
// against its meta (count + digest) and reports, per secret, what Restore would
// do against the live LocalStack. It never creates, changes or deletes anything,
// and never touches the restore marker. With all=false only the newest readable
// snapshot is checked; with all=true every snapshot must decrypt and verify.
func (t *Tool) Verify(ctx context.Context, all bool) error {
	if err := RequireSeedKey(t.cfg.KeyFile); err != nil {
		return err
	}
	live, err := t.liveByName(ctx)
	if err != nil {
		return err
	}
	if !all {
		found, err := t.newestReadable(ctx)
		if err != nil {
			return err
		}
		if found == nil {
			t.log.Printf("verify: no snapshot in %s", t.cfg.Dir)
			return nil
		}
		t.reportAgainstLive(found, live)
		return nil
	}
	return t.verifyEverySnapshot(ctx, live)
}

func (t *Tool) verifyEverySnapshot(ctx context.Context, live map[string]Entry) error {
	metas, err := t.snapshotMetas()
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		t.log.Printf("verify: no snapshot in %s", t.cfg.Dir)
		return nil
	}
	unreadable := 0
	for _, file := range metas {
		secrets, err := t.loadSnapshot(ctx, file)
		if err != nil {
			unreadable++
			t.log.Printf("verify: %s is NOT readable: %s", file.Name(), safeMessage(err))
			continue
		}
		t.reportAgainstLive(&loadedSnapshot{Name: file.Name(), Meta: file.Meta, Secrets: secrets}, live)
	}
	if unreadable > 0 {
		return safeErrorf("%d of %d snapshot(s) could not be read", unreadable, len(metas))
	}
	return nil
}

func (t *Tool) liveByName(ctx context.Context) (map[string]Entry, error) {
	entries, err := t.CollectLive(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	return byName, nil
}

func (t *Tool) reportAgainstLive(found *loadedSnapshot, live map[string]Entry) {
	outcomes := make(map[string][]string)
	inSnapshot := make(map[string]struct{}, len(found.Secrets))
	for _, entry := range found.Secrets {
		inSnapshot[entry.Name] = struct{}{}
		outcome := OutcomeMissing
		if current, ok := live[entry.Name]; ok {
			outcome = compareLive(current, entry)
		}
		outcomes[outcome] = append(outcomes[outcome], entry.Name)
	}
	notInSnapshot := 0
	for name := range live {
		if _, ok := inSnapshot[name]; !ok {
			notInSnapshot++
		}
	}
	t.logOutcomes(outcomes, found, "verify (read-only)")
	t.log.Printf("verify %s: digest and count match its meta; live=%d not_in_snapshot=%d",
		found.Name, len(live), notInSnapshot)
}
