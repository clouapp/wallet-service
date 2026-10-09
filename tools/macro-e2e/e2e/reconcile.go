package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	ExitNotConfirmedYet = 2

	reconcileOwner       = "ledger-reconcile"
	reconcileNoteJoiner  = "; "
	onchainLegKey        = "onchain"
	verifiedEntryKey     = "verified"
	confirmedAtEntryKey  = "confirmedAt"
	claimWalletKey       = "wallet_id"
	claimedAtKey         = "claimedAt"
	responseFileEntryKey = "response_file"
)

// Reconcile fills in the tx hashes of consolidate entries from vault_test (SELECT only)
// and checks each hash on chain (read-only). Dry run by default; Apply holds the recording
// lock and the ledger flock, backs ledger.json up next to it (0600) and rewrites the
// entries in place. Nothing is ever sent to the API or to a chain.
type Reconcile struct {
	Paths     Paths
	Ledger    FundingLedger
	Recording RecordingLock
	SweepRows func(ctx context.Context, query SweepQuery) ([]SweepRow, error)
	Verify    func(ctx context.Context, chain, hash string) (Verification, error)
	Now       func() time.Time
	Out       io.Writer
}

// ReconcileRequest names the ledger tags to reconcile.
type ReconcileRequest struct {
	Tags  []string
	Apply bool
}

type reconciledEntry struct {
	entry     pyjson.Object
	confirmed bool
}

// Run exits 0 when every entry is confirmed, 2 when some are not confirmed yet, 1 on errors
// (then nothing is written).
func (reconcile Reconcile) Run(ctx context.Context, request ReconcileRequest) (int, error) {
	if len(request.Tags) == 0 {
		return ExitFailure, fmt.Errorf("ledger-reconcile needs at least one tag")
	}
	seen := map[string]bool{}
	for _, tag := range request.Tags {
		if _, err := Require(TagPattern, tag, "tag"); err != nil {
			return ExitFailure, err
		}
		if seen[tag] {
			return ExitFailure, fmt.Errorf("tag %s given twice", tag)
		}
		seen[tag] = true
	}
	if request.Apply {
		if err := reconcile.Recording.Acquire(reconcileOwner); err != nil {
			return ExitFailure, err
		}
		defer reconcile.Recording.Release(reconcileOwner)
		lock, err := reconcile.Ledger.Lock(ctx)
		if err != nil {
			return ExitFailure, err
		}
		defer lock.Release()
	}
	_, entries, err := reconcile.Ledger.Load()
	if err != nil {
		return ExitFailure, err
	}
	results := make(map[string]reconciledEntry, len(request.Tags))
	for _, tag := range request.Tags {
		entry, err := findLedgerEntry(entries, tag)
		if err != nil {
			return ExitFailure, err
		}
		result, err := reconcile.entry(ctx, tag, entry)
		if err != nil {
			return ExitFailure, fmt.Errorf("%s: %w", tag, err)
		}
		results[tag] = result
	}
	allConfirmed := true
	for _, result := range results {
		allConfirmed = allConfirmed && result.confirmed
	}
	if !request.Apply {
		fmt.Fprintln(reconcile.Out, "dry run: ledger not written (add --apply to back it up and update these entries)")
	} else {
		backup, err := reconcile.Ledger.Backup(reconcile.Now())
		if err != nil {
			return ExitFailure, err
		}
		replacements := make(map[string]pyjson.Object, len(results))
		for tag, result := range results {
			replacements[tag] = result.entry
		}
		if err := reconcile.Ledger.ReplaceEntries(replacements); err != nil {
			return ExitFailure, fmt.Errorf("%w (backup %s)", err, backup)
		}
		fmt.Fprintf(reconcile.Out, "ledger backup %s\nupdated %d entries in %s\n", backup, len(replacements), reconcile.Ledger.Path)
	}
	if !allConfirmed {
		return ExitNotConfirmedYet, nil
	}
	return ExitOK, nil
}

func findLedgerEntry(entries []any, tag string) (pyjson.Object, error) {
	for _, entry := range entries {
		if object, isObject := entry.(pyjson.Object); isObject && object.String(ledgerTagKey) == tag {
			return object, nil
		}
	}
	return nil, fmt.Errorf("funding ledger has no entry %s", tag)
}

func (reconcile Reconcile) entry(ctx context.Context, tag string, entry pyjson.Object) (reconciledEntry, error) {
	legs := sweepLegs(entry)
	if len(legs) == 0 {
		return reconciledEntry{}, fmt.Errorf("not a consolidate entry (no sweeps)")
	}
	query, responseHashes, err := reconcile.sweepContext(tag, entry)
	if err != nil {
		return reconciledEntry{}, err
	}
	rows, err := reconcile.SweepRows(ctx, query)
	if err != nil {
		return reconciledEntry{}, err
	}
	matches := MatchSweepLegs(legs, rows, responseHashes)
	fmt.Fprintf(reconcile.Out, "%s (wallet %s, status %s)\n", tag, query.WalletID, pythonValueRepr(entry.String("status")))

	confirmed := allHashesKnown(matches) && allRowsConfirmed(matches)
	notes := make([]string, 0, len(matches))
	updated := withSweepHashes(entry, matches)
	updatedLegs := sweepLegs(updated)
	sweeps := make([]any, 0, len(matches))
	for index, match := range matches {
		leg := updatedLegs[index]
		note := "not checked (no tx hash in vault_test yet)"
		if match.Row != nil && match.Row.TxHash != "" {
			if match.ResponseHash != "" && !strings.EqualFold(match.ResponseHash, match.Row.TxHash) {
				return reconciledEntry{}, fmt.Errorf("leg %d: vault_test hash %s differs from the consolidate response hash %s", index, match.Row.TxHash, match.ResponseHash)
			}
			verification, err := reconcile.Verify(ctx, match.Row.Chain, match.Row.TxHash)
			if err != nil {
				return reconciledEntry{}, fmt.Errorf("leg %d on-chain check: %w", index, err)
			}
			note = verification.Note
			confirmed = confirmed && verification.Confirmed
			leg = leg.Set(onchainLegKey, note)
		}
		fmt.Fprintf(reconcile.Out, "  %s; on-chain: %s\n", describeSweepMatch(index, match), note)
		notes = append(notes, note)
		sweeps = append(sweeps, leg)
	}
	updated = updated.Set("sweeps", sweeps)
	if previous := entry.String("hash"); previous != "" && updated.String("hash") != "" && !strings.EqualFold(previous, updated.String("hash")) {
		return reconciledEntry{}, fmt.Errorf("ledger hash %s differs from vault_test %s", previous, updated.String("hash"))
	}
	if !confirmed {
		fmt.Fprintf(reconcile.Out, "  => not confirmed yet; status stays %s\n", pythonValueRepr(entry.String("status")))
		return reconciledEntry{entry: updated}, nil
	}
	now := NowISO(reconcile.Now())
	updated = updated.Set("status", statusConfirmed).
		Set(confirmedAtEntryKey, now).
		Set(verifiedEntryKey, "vault_test confirmed; on-chain "+strings.Join(notes, reconcileNoteJoiner)+" (ledger-reconcile "+now+")")
	fmt.Fprintf(reconcile.Out, "  => confirmed, hash %s\n", updated.String("hash"))
	return reconciledEntry{entry: updated, confirmed: true}, nil
}

// sweepContext reads the wallet and request time from the claim (<locks>/send-<tag>.claim)
// and the response hashes from the entry's response_file (inside the funding dir). The
// vault_test window is [claimedAt, entry recordedAt], widened by SweepWindowSlack.
func (reconcile Reconcile) sweepContext(tag string, entry pyjson.Object) (SweepQuery, map[string]string, error) {
	claimPath := filepath.Join(reconcile.Paths.LocksDir, claimFilePrefix+tag+claimFileSuffix)
	claim, err := readJSONObject(claimPath)
	if err != nil {
		return SweepQuery{}, nil, err
	}
	walletID, err := Require(UUIDPattern, claim.String(claimWalletKey), "claim wallet id")
	if err != nil {
		return SweepQuery{}, nil, err
	}
	claimedAt, err := parseISOTimestamp(claim.String(claimedAtKey))
	if err != nil {
		return SweepQuery{}, nil, fmt.Errorf("%s has no readable %s", claimPath, claimedAtKey)
	}
	query := SweepQuery{WalletID: walletID, CreatedFrom: claimedAt.Add(-SweepWindowSlack)}
	if recordedAt, err := parseISOTimestamp(entry.String(ledgerRecordedAtKey)); err == nil {
		query.CreatedUntil = recordedAt.Add(SweepWindowSlack)
	}

	responseHashes := map[string]string{}
	responseFile := entry.String(responseFileEntryKey)
	if responseFile == "" {
		return query, responseHashes, nil
	}
	if filepath.Dir(filepath.Clean(responseFile)) != filepath.Clean(reconcile.Paths.FundingDir) {
		return SweepQuery{}, nil, fmt.Errorf("response_file %s is outside %s", responseFile, reconcile.Paths.FundingDir)
	}
	record, err := readJSONObject(responseFile)
	if err != nil {
		return SweepQuery{}, nil, err
	}
	plan, _ := record.Get("plan")
	if planObject, isObject := plan.(pyjson.Object); isObject && planObject.String(claimWalletKey) != walletID {
		return SweepQuery{}, nil, fmt.Errorf("%s names wallet %s, the claim %s", responseFile, pythonRepr(planObject.String(claimWalletKey)), walletID)
	}
	response, _ := record.Get("response")
	return query, ResponseSweepHashes(response), nil
}

func readJSONObject(path string) (pyjson.Object, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s is missing", path)
	}
	object, err := decodeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	return object, nil
}
