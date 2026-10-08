package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/pkg/pyjson"
)

const (
	ExitOK          = 0
	ExitFailure     = 1
	ExitNoTxHashYet = 2

	TxHashPollInterval = 10 * time.Second
	TxHashWait         = 600 * time.Second

	sendOwnerPrefix         = "send-from-base:"
	consolidateOwnerPrefix  = "consolidate-once:"
	sendPurpose             = "e2e funding transfer from base via Wallets API"
	consolidatePurpose      = "child-address sweep into the base via Wallets API"
	statusSubmitted         = "submitted"
	statusBroadcast         = "broadcast"
	statusNoTxHashYet       = "submitted, no tx hash yet"
	fundingFileSuffix       = ".json"
	plannedPreviewRunes     = 600
	errorResponseRunes      = 800
	consolidateResultRunes  = 2000
	consolidateResultIndent = 1
	firstErrorStatus        = 300
	noTxHashYet             = "(none yet)"
)

var sweepKeys = []string{"from", "to", "amount"}

// Funding runs the guarded one-shot transfers through the external Wallets API.
// Guards, all before the single POST: no ledger entry with the tag (and, for sends, no
// matching outbound transaction in vault_test); the recording lock; an exclusive,
// never-removed claim per tag; a fresh off-chain pre-flight of the same transfer. The
// idempotency key is a UUIDv5 of the tag, so a repeated POST returns the original one.
// Token and passphrase stay in memory and are never printed.
type Funding struct {
	Paths       Paths
	Preflight   func(ctx context.Context, request PreflightRequest) (pyjson.Object, error)
	Passphrases PassphraseSource
	Services    Services
	Ledger      FundingLedger
	Recording   RecordingLock
	Now         func() time.Time
	Sleep       func(ctx context.Context, duration time.Duration) error
	PollEvery   time.Duration
	PollFor     time.Duration
	// SweepPollEvery and SweepPollFor bound the vault_test poll for the sweep tx hashes.
	SweepPollEvery time.Duration
	SweepPollFor   time.Duration
	Out            io.Writer
	Err            io.Writer
}

// SendRequest is one transfer from a base address.
type SendRequest struct {
	Tag                 string
	WalletID            string
	Asset               string
	BaseUnits           string
	Decimals            string
	To                  string
	ExternalUserID      string
	Chain               string
	RecordingLockHeldBy string
	Apply               bool
}

// ConsolidateRequest is one child-address sweep into the base.
type ConsolidateRequest struct {
	Tag      string
	WalletID string
	Asset    string
	// Chain names the ledger chain when the asset is a token (USDT on tron); empty means lower(asset).
	Chain string
	Apply bool
}

func (request ConsolidateRequest) ledgerChain() (string, error) {
	if request.Chain == "" {
		return strings.ToLower(request.Asset), nil
	}
	return Require(ChainPattern, request.Chain, "chain")
}

type validatedSend struct {
	SendRequest
	ledgerChain    string
	humanAmount    string
	idempotencyKey string
}

func (request SendRequest) validate() (validatedSend, error) {
	ledgerChain := strings.ToLower(request.Asset)
	if request.Chain != "" {
		if _, err := Require(ChainPattern, request.Chain, "chain"); err != nil {
			return validatedSend{}, err
		}
		ledgerChain = request.Chain
	}
	if request.RecordingLockHeldBy != "" {
		if _, err := Require(RecordingOwnerPattern, request.RecordingLockHeldBy, "recording lock owner"); err != nil {
			return validatedSend{}, err
		}
	}
	if err := requireAll([]fieldCheck{
		{TagPattern, request.Tag, "tag"},
		{UUIDPattern, request.WalletID, "wallet id"},
		{AssetPattern, request.Asset, "asset"},
		{AmountPattern, request.BaseUnits, "amount (base units)"},
		{AddressPattern, request.To, "destination address"},
		{ExternalUserPattern, request.ExternalUserID, "external user id"},
	}); err != nil {
		return validatedSend{}, err
	}
	decimals, err := ParseDecimals(request.Decimals)
	if err != nil {
		return validatedSend{}, err
	}
	human, err := HumanAmount(request.BaseUnits, decimals)
	if err != nil {
		return validatedSend{}, err
	}
	return validatedSend{SendRequest: request, ledgerChain: ledgerChain, humanAmount: human, idempotencyKey: IdempotencyKey(request.Tag)}, nil
}

// SendFromBase plans (and with Apply, sends once) a transfer from a base address.
// Exit codes: 0 done (or dry run), 1 refused/failed, 2 submitted without a tx hash yet.
func (funding Funding) SendFromBase(ctx context.Context, request SendRequest) (int, error) {
	send, err := request.validate()
	if err != nil {
		return ExitFailure, err
	}
	if alreadyFunded, err := funding.Ledger.HasTag(send.Tag); err != nil {
		return ExitFailure, err
	} else if alreadyFunded {
		return ExitFailure, fmt.Errorf("funding ledger already has %s; not sending again", send.Tag)
	}
	existing, err := funding.Services.OutboundMatches(ctx, send.WalletID, send.To, send.Asset, send.BaseUnits)
	if err != nil {
		return ExitFailure, err
	}
	if len(existing) > 0 {
		return ExitFailure, fmt.Errorf("vault_test already has this outbound transfer: %s", pythonStringListRepr(existing))
	}
	preflight, err := funding.Preflight(ctx, PreflightRequest{Mode: PreflightModeWithdrawal, WalletID: send.WalletID, Asset: send.Asset, Amount: send.BaseUnits, To: send.To})
	if err != nil {
		return ExitFailure, err
	}
	transfers := PreflightTransactions(preflight)
	if len(transfers) != 1 || transfers[0].String("to") != send.To || transfers[0].String("amount") != send.BaseUnits {
		planned, _ := preflight.Get("transactions")
		return ExitFailure, fmt.Errorf("pre-flight planned something else: %s", truncateRunes(dumpsOrEmpty(orEmptyList(planned)), plannedPreviewRunes))
	}
	from, _ := transfers[0].Get("from")
	plan := pyjson.Object{
		{Key: "tag", Value: send.Tag},
		{Key: "wallet_id", Value: send.WalletID},
		{Key: "asset", Value: send.Asset},
		{Key: "amount", Value: send.humanAmount},
		{Key: "base_units", Value: send.BaseUnits},
		{Key: "to", Value: send.To},
		{Key: "from", Value: from},
		{Key: "idempotency_key", Value: send.idempotencyKey},
	}
	fmt.Fprintln(funding.Out, "plan "+dumpsOrEmpty(plan))
	if !send.Apply {
		fmt.Fprintln(funding.Out, "dry run: pre-flight verified, nothing sent (add --apply to send once)")
		return ExitOK, nil
	}

	owner := sendOwnerPrefix + send.Tag
	if send.RecordingLockHeldBy != "" {
		if err := funding.Recording.RequireHeldBy(send.RecordingLockHeldBy); err != nil {
			return ExitFailure, err
		}
	} else {
		if err := funding.Recording.Acquire(owner); err != nil {
			return ExitFailure, err
		}
		defer funding.Recording.Release(owner)
	}
	return funding.sendOnce(ctx, send, plan, from)
}

func (funding Funding) sendOnce(ctx context.Context, send validatedSend, plan pyjson.Object, from any) (int, error) {
	claim, err := ClaimTransfer(funding.Paths.LocksDir, send.Tag, plan, funding.Now())
	if err != nil {
		return ExitFailure, err
	}
	token, passphrase, err := funding.credentials(ctx, send.WalletID)
	if err != nil {
		return ExitFailure, err
	}
	status, response, err := funding.Services.API.Request(ctx, http.MethodPost, "/api/v1/wallets/"+send.WalletID+"/withdrawals", token, pyjson.Object{
		{Key: "external_user_id", Value: send.ExternalUserID},
		{Key: "destination_address", Value: send.To},
		{Key: "amount", Value: send.humanAmount},
		{Key: "asset", Value: send.Asset},
		{Key: "passphrase", Value: passphrase},
		{Key: "idempotency_key", Value: send.idempotencyKey},
	})
	if err != nil {
		return ExitFailure, fmt.Errorf("withdrawal POST failed (claim %s stays; review before anything else): %w", claim, err)
	}
	entry := pyjson.Object{
		{Key: "chain", Value: send.ledgerChain},
		{Key: "tag", Value: send.Tag},
		{Key: "purpose", Value: sendPurpose},
		{Key: "source", Value: from},
		{Key: "destination", Value: send.To},
		{Key: "amount", Value: fmt.Sprintf("%s base units (%s %s)", send.BaseUnits, send.humanAmount, send.Asset)},
		{Key: "idempotency_key", Value: send.idempotencyKey},
		{Key: "http_status", Value: status},
		{Key: "hash", Value: FindTxHash(response)},
		{Key: "status", Value: statusSubmitted},
		{Key: "recordedAt", Value: NowISO(funding.Now())},
	}
	if err := funding.Ledger.Upsert(send.Tag, entry); err != nil {
		return ExitFailure, err
	}
	if err := funding.writeFundingRecord(send.Tag, plan, status, response); err != nil {
		return ExitFailure, err
	}
	fmt.Fprintf(funding.Out, "POST status %d; claim %s\n", status, claim)
	if status >= firstErrorStatus {
		fmt.Fprintln(funding.Err, truncateRunes(dumpsOrEmpty(response), errorResponseRunes))
		return ExitFailure, nil
	}

	entry = funding.waitForTxHash(ctx, token, send, entry)
	hash := entry.String("hash")
	entryStatus := statusNoTxHashYet
	if hash != "" {
		entryStatus = statusBroadcast
	}
	entry = entry.Set("status", entryStatus).Set("recordedAt", NowISO(funding.Now()))
	if err := funding.Ledger.Upsert(send.Tag, entry); err != nil {
		return ExitFailure, err
	}
	if hash == "" {
		fmt.Fprintf(funding.Out, "tx hash: %s\n", noTxHashYet)
		return ExitNoTxHashYet, nil
	}
	fmt.Fprintf(funding.Out, "tx hash: %s\n", hash)
	return ExitOK, nil
}

func (funding Funding) waitForTxHash(ctx context.Context, token string, send validatedSend, entry pyjson.Object) pyjson.Object {
	deadline := funding.Now().Add(funding.PollFor)
	lookupPath := "/api/v1/wallets/" + send.WalletID + "/withdrawals/" + send.idempotencyKey
	for entry.String("hash") == "" && funding.Now().Before(deadline) {
		if err := funding.Sleep(ctx, funding.PollEvery); err != nil {
			return entry
		}
		_, lookup, err := funding.Services.API.Request(ctx, http.MethodGet, lookupPath, token, nil)
		if err != nil {
			fmt.Fprintf(funding.Err, "withdrawal lookup failed (will retry): %v\n", err)
			continue
		}
		entry = entry.Set("hash", FindTxHash(lookup)).Set("lookup_status", LookupStatus(lookup))
	}
	return entry
}

// Consolidate plans (and with Apply, posts once) a child-address sweep into the base.
func (funding Funding) Consolidate(ctx context.Context, request ConsolidateRequest) (int, error) {
	if err := requireAll([]fieldCheck{{TagPattern, request.Tag, "tag"}, {UUIDPattern, request.WalletID, "wallet id"}, {AssetPattern, request.Asset, "asset"}}); err != nil {
		return ExitFailure, err
	}
	if _, err := request.ledgerChain(); err != nil {
		return ExitFailure, err
	}
	idempotencyKey := IdempotencyKey(request.Tag)
	if alreadyFunded, err := funding.Ledger.HasTag(request.Tag); err != nil {
		return ExitFailure, err
	} else if alreadyFunded {
		return ExitFailure, fmt.Errorf("funding ledger already has %s; not consolidating again", request.Tag)
	}
	preflight, err := funding.Preflight(ctx, PreflightRequest{Mode: PreflightModeConsolidation, WalletID: request.WalletID, Asset: request.Asset})
	if err != nil {
		return ExitFailure, err
	}
	transactions := PreflightTransactions(preflight)
	if len(transactions) == 0 {
		return ExitFailure, fmt.Errorf("pre-flight planned no sweep; nothing to consolidate")
	}
	sweeps := make([]any, 0, len(transactions))
	for _, transaction := range transactions {
		sweep := pyjson.Object{}
		for _, key := range sweepKeys {
			value, _ := transaction.Get(key)
			sweep = sweep.Set(key, value)
		}
		sweeps = append(sweeps, sweep)
	}
	strategy, _ := preflight.Get("strategy")
	plan := pyjson.Object{
		{Key: "tag", Value: request.Tag},
		{Key: "wallet_id", Value: request.WalletID},
		{Key: "asset", Value: request.Asset},
		{Key: "strategy", Value: strategy},
		{Key: "sweeps", Value: sweeps},
		{Key: "idempotency_key", Value: idempotencyKey},
	}
	fmt.Fprintln(funding.Out, "plan "+dumpsOrEmpty(plan))
	if !request.Apply {
		fmt.Fprintln(funding.Out, "dry run: pre-flight verified, nothing sent (add --apply to consolidate once)")
		return ExitOK, nil
	}

	owner := consolidateOwnerPrefix + request.Tag
	if err := funding.Recording.Acquire(owner); err != nil {
		return ExitFailure, err
	}
	defer funding.Recording.Release(owner)
	return funding.consolidateOnce(ctx, request, plan, sweeps, idempotencyKey)
}

func (funding Funding) consolidateOnce(ctx context.Context, request ConsolidateRequest, plan pyjson.Object, sweeps []any, idempotencyKey string) (int, error) {
	claim, err := ClaimTransfer(funding.Paths.LocksDir, request.Tag, plan, funding.Now())
	if err != nil {
		return ExitFailure, err
	}
	token, passphrase, err := funding.credentials(ctx, request.WalletID)
	if err != nil {
		return ExitFailure, err
	}
	requestedAt := funding.Now()
	status, response, err := funding.Services.API.Request(ctx, http.MethodPost, "/api/v1/wallets/"+request.WalletID+"/consolidate", token, pyjson.Object{
		{Key: "asset", Value: request.Asset},
		{Key: "passphrase", Value: passphrase},
		{Key: "idempotency_key", Value: idempotencyKey},
	})
	if err != nil {
		return ExitFailure, fmt.Errorf("consolidate POST failed (claim %s stays; review before anything else): %w", claim, err)
	}
	if err := funding.writeFundingRecord(request.Tag, plan, status, response); err != nil {
		return ExitFailure, err
	}
	ledgerChain, err := request.ledgerChain()
	if err != nil {
		return ExitFailure, err
	}
	entry := pyjson.Object{
		{Key: "chain", Value: ledgerChain},
		{Key: "tag", Value: request.Tag},
		{Key: "purpose", Value: consolidatePurpose},
		{Key: "sweeps", Value: sweeps},
		{Key: "idempotency_key", Value: idempotencyKey},
		{Key: "http_status", Value: status},
		{Key: "response_file", Value: funding.fundingRecordPath(request.Tag)},
		{Key: "status", Value: statusSubmitted},
		{Key: "recordedAt", Value: NowISO(funding.Now())},
	}
	if err := funding.Ledger.Upsert(request.Tag, entry); err != nil {
		return ExitFailure, err
	}
	fmt.Fprintf(funding.Out, "POST status %d; claim %s\n", status, claim)
	indented, err := pyjson.DumpsIndent(response, consolidateResultIndent)
	if err != nil {
		indented = dumpsOrEmpty(response)
	}
	fmt.Fprintln(funding.Out, truncateRunes(indented, consolidateResultRunes))
	if status >= firstErrorStatus {
		return ExitFailure, nil
	}
	return funding.recordSweepHashes(ctx, request, entry, requestedAt, response)
}

// recordSweepHashes polls vault_test (never the API again) until every planned leg has a
// row with a tx hash, then writes the hashes into the ledger entry: status confirmed once
// every row is confirmed, else submitted (ledger-reconcile confirms it later).
func (funding Funding) recordSweepHashes(ctx context.Context, request ConsolidateRequest, entry pyjson.Object, requestedAt time.Time, response any) (int, error) {
	query := SweepQuery{WalletID: request.WalletID, CreatedFrom: requestedAt.Add(-SweepWindowSlack)}
	matches := funding.awaitSweepRows(ctx, query, sweepLegs(entry), ResponseSweepHashes(response))
	for index, match := range matches {
		fmt.Fprintln(funding.Out, describeSweepMatch(index, match))
	}
	entryStatus := statusSubmitted
	switch {
	case !allHashesKnown(matches):
		entryStatus = statusNoTxHashYet
	case allRowsConfirmed(matches):
		entryStatus = statusConfirmed
	}
	entry = withSweepHashes(entry, matches).Set("status", entryStatus).Set("recordedAt", NowISO(funding.Now()))
	if err := funding.Ledger.Upsert(request.Tag, entry); err != nil {
		return ExitFailure, err
	}
	switch entryStatus {
	case statusNoTxHashYet:
		fmt.Fprintf(funding.Out, "tx hash: %s for every leg after %s; ledger status %q\n", noTxHashYet, funding.SweepPollFor, entryStatus)
		return ExitNoTxHashYet, nil
	case statusSubmitted:
		fmt.Fprintf(funding.Out, "tx hash: %s; vault_test has not confirmed every leg yet, ledger status stays %q (run ledger-reconcile %s later)\n", entry.String("hash"), entryStatus, request.Tag)
	default:
		fmt.Fprintf(funding.Out, "tx hash: %s; confirmed in vault_test\n", entry.String("hash"))
	}
	return ExitOK, nil
}

func (funding Funding) awaitSweepRows(ctx context.Context, query SweepQuery, legs []pyjson.Object, responseHashes map[string]string) []SweepMatch {
	deadline := funding.Now().Add(funding.SweepPollFor)
	for {
		rows, err := funding.Services.SweepRows(ctx, query)
		if err != nil {
			fmt.Fprintf(funding.Err, "vault_test sweep lookup failed (will retry): %v\n", err)
		}
		matches := MatchSweepLegs(legs, rows, responseHashes)
		if allHashesKnown(matches) || !funding.Now().Before(deadline) {
			return matches
		}
		if err := funding.Sleep(ctx, funding.SweepPollEvery); err != nil {
			return matches
		}
	}
}

func (funding Funding) credentials(ctx context.Context, walletID string) (string, string, error) {
	token, err := funding.Services.MarketsToken(ctx)
	if err != nil {
		return "", "", err
	}
	parsedWalletID, err := uuid.Parse(walletID)
	if err != nil {
		return "", "", fmt.Errorf("invalid wallet id: %s", pythonRepr(walletID))
	}
	passphrase, err := funding.Passphrases.Read(ctx, parsedWalletID)
	if err != nil {
		return "", "", err
	}
	return token, passphrase, nil
}

func (funding Funding) fundingRecordPath(tag string) string {
	return filepath.Join(funding.Paths.FundingDir, tag+fundingFileSuffix)
}

func (funding Funding) writeFundingRecord(tag string, plan pyjson.Object, status int, response any) error {
	return writeIndentedJSON(funding.fundingRecordPath(tag), pyjson.Object{
		{Key: "plan", Value: plan},
		{Key: "status", Value: status},
		{Key: "response", Value: response},
	})
}

// SleepContext waits for duration or until ctx is done.
func SleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func dumpsOrEmpty(value any) string {
	encoded, err := pyjson.Dumps(value, pyjson.Default)
	if err != nil {
		return emptyJSONObject
	}
	return encoded
}

func orEmptyList(value any) any {
	if pythonTruthy(value) {
		return value
	}
	return []any{}
}

func pythonStringListRepr(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, pythonRepr(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
