package e2e

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	SweepPollInterval = 5 * time.Second
	SweepPollWait     = 180 * time.Second
	// SweepWindowSlack widens the created_at window: the column is timestamp(0) in UTC and
	// the API clock is not this process's clock.
	SweepWindowSlack = 5 * time.Second

	vaultTimestampLayout = "2006-01-02 15:04:05"
	vaultFieldSeparator  = "\x1f"
	vaultReadOnlyOption  = "PGOPTIONS=-c default_transaction_read_only=on"
	vaultStatusConfirmed = "confirmed"
	sweepRowFieldCount   = 11
	txTypeSweep          = "sweep"
	statusConfirmed      = "confirmed"
)

// TxHashPattern is a 32-byte hex transaction hash, optionally 0x-prefixed (EVM).
var TxHashPattern = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{64}$`)

// SweepRow is one vault_test transactions row of a consolidation leg (a sweep or its gas seed).
type SweepRow struct {
	ID, Chain, TxType, Origin, Status, TxHash, From, To, Amount, Asset, CreatedAt string
}

// SweepQuery selects the sweep and gas_seed rows of a wallet created in [CreatedFrom,
// CreatedUntil]; a zero CreatedUntil leaves the window open.
type SweepQuery struct {
	WalletID     string
	CreatedFrom  time.Time
	CreatedUntil time.Time
}

// SweepMatch pairs one planned leg ({"from", "to", "amount"}) with its vault_test row
// (nil while none is found) and the tx hash the consolidate response reported for it.
type SweepMatch struct {
	Leg          pyjson.Object
	Row          *SweepRow
	ResponseHash string
}

// Hash is the row's tx hash, else the hash of the consolidate response.
func (match SweepMatch) Hash() string {
	if match.Row != nil && match.Row.TxHash != "" {
		return match.Row.TxHash
	}
	return match.ResponseHash
}

// SweepRows reads (SELECT only, in a read-only transaction) the sweep rows of query.
func (docker DockerServices) SweepRows(ctx context.Context, query SweepQuery) ([]SweepRow, error) {
	if _, err := Require(UUIDPattern, query.WalletID, "wallet id"); err != nil {
		return nil, err
	}
	if query.CreatedFrom.IsZero() {
		return nil, fmt.Errorf("sweep lookup needs a created_at lower bound")
	}
	window := "created_at >= '" + query.CreatedFrom.UTC().Format(vaultTimestampLayout) + "'"
	if !query.CreatedUntil.IsZero() {
		window += " and created_at <= '" + query.CreatedUntil.UTC().Format(vaultTimestampLayout) + "'"
	}
	sql := "select id, chain, tx_type, coalesce(origin,''), status, coalesce(tx_hash,''), coalesce(from_address,''), to_address, amount, asset, " +
		"to_char(created_at, 'YYYY-MM-DD HH24:MI:SS') from transactions " +
		"where wallet_id = '" + query.WalletID + "' and tx_type in ('sweep','gas_seed') and " + window + " order by created_at, id"
	lines, err := docker.queryVaultTest(ctx, sql)
	if err != nil {
		return nil, err
	}
	rows := make([]SweepRow, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, vaultFieldSeparator)
		if len(fields) != sweepRowFieldCount {
			return nil, fmt.Errorf("vault_test sweep row has %d fields, want %d", len(fields), sweepRowFieldCount)
		}
		rows = append(rows, SweepRow{
			ID: fields[0], Chain: fields[1], TxType: fields[2], Origin: fields[3], Status: fields[4], TxHash: fields[5],
			From: fields[6], To: fields[7], Amount: fields[8], Asset: fields[9], CreatedAt: fields[10],
		})
	}
	return rows, nil
}

// queryVaultTest runs one SELECT through psql in the Wallets DB container, inside a
// read-only transaction, and returns the non-empty unaligned output lines.
func (docker DockerServices) queryVaultTest(ctx context.Context, sql string) ([]string, error) {
	queryContext, cancel := context.WithTimeout(ctx, vaultQueryTimeout)
	defer cancel()
	args := []string{"exec", "-e", vaultReadOnlyOption, docker.WalletsDBContainer, "psql", "-U", walletsDBUser, "-d", E2EDatabase, "-At", "-F", vaultFieldSeparator, "-c", sql}
	result, err := docker.Run(queryContext, dockerBinary, args, "", os.Environ(), nil)
	if err != nil {
		return nil, fmt.Errorf("vault_test query failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("vault_test query failed: %s", lastRunes(strings.TrimSpace(string(result.Stderr)), queryErrorTailRunes))
	}
	var lines []string
	for _, line := range splitPythonLines(string(result.Stdout)) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// ResponseSweepHashes maps lower(from) to the tx_hash of each response.transactions item.
func ResponseSweepHashes(response any) map[string]string {
	hashes := map[string]string{}
	root, isObject := response.(pyjson.Object)
	if !isObject {
		return hashes
	}
	items, _ := root.Get("transactions")
	list, _ := items.([]any)
	for _, item := range list {
		transaction, isObject := item.(pyjson.Object)
		if !isObject {
			continue
		}
		hash := transaction.String("tx_hash")
		if from := strings.ToLower(transaction.String("from")); from != "" && hash != "" {
			hashes[from] = hash
		}
	}
	return hashes
}

// MatchSweepLegs pairs each leg with an unused row of the same from/to addresses,
// preferring the row carrying the response hash, then the planned amount, then the
// oldest. Amounts are only a preference: the executor re-quotes fees, so a native
// sweep usually stores a different amount than the pre-flight planned.
func MatchSweepLegs(legs []pyjson.Object, rows []SweepRow, responseHashes map[string]string) []SweepMatch {
	used := make([]bool, len(rows))
	matches := make([]SweepMatch, 0, len(legs))
	for _, leg := range legs {
		from, to := leg.String("from"), leg.String("to")
		match := SweepMatch{Leg: leg, ResponseHash: responseHashes[strings.ToLower(from)]}
		best, bestScore := -1, -1
		for index, row := range rows {
			if used[index] || !strings.EqualFold(row.From, from) || !strings.EqualFold(row.To, to) {
				continue
			}
			score := 0
			if match.ResponseHash != "" && strings.EqualFold(row.TxHash, match.ResponseHash) {
				score += 2
			}
			if amount := leg.String("amount"); amount != "" && row.Amount == amount {
				score++
			}
			if score > bestScore {
				best, bestScore = index, score
			}
		}
		if best >= 0 {
			used[best] = true
			row := rows[best]
			match.Row = &row
		}
		matches = append(matches, match)
	}
	return matches
}

// sweepLegs returns the planned legs of a ledger entry or plan ("sweeps").
func sweepLegs(holder pyjson.Object) []pyjson.Object {
	value, _ := holder.Get("sweeps")
	items, _ := value.([]any)
	legs := make([]pyjson.Object, 0, len(items))
	for _, item := range items {
		leg, _ := item.(pyjson.Object)
		legs = append(legs, leg)
	}
	return legs
}

func allHashesKnown(matches []SweepMatch) bool {
	for _, match := range matches {
		if match.Row == nil || match.Row.TxHash == "" {
			return false
		}
	}
	return len(matches) > 0
}

func allRowsConfirmed(matches []SweepMatch) bool {
	for _, match := range matches {
		if match.Row == nil || match.Row.Status != vaultStatusConfirmed {
			return false
		}
	}
	return len(matches) > 0
}

// primarySweepHash is the hash of the first child sweep leg (not its gas seed), else the first hash.
func primarySweepHash(matches []SweepMatch) string {
	for _, match := range matches {
		if match.Row != nil && match.Row.TxType == txTypeSweep && match.Hash() != "" {
			return match.Hash()
		}
	}
	for _, match := range matches {
		if hash := match.Hash(); hash != "" {
			return hash
		}
	}
	return ""
}

// withSweepHashes writes tx_hash and vault_status into each leg of entry.sweeps and the
// primary hash into entry.hash (the key the Markets e2e reads).
func withSweepHashes(entry pyjson.Object, matches []SweepMatch) pyjson.Object {
	sweeps := make([]any, 0, len(matches))
	for _, match := range matches {
		leg := match.Leg
		if hash := match.Hash(); hash != "" {
			leg = leg.Set("tx_hash", hash)
		}
		if match.Row != nil {
			leg = leg.Set("vault_status", match.Row.Status)
		}
		sweeps = append(sweeps, leg)
	}
	entry = entry.Set("sweeps", sweeps)
	if hash := primarySweepHash(matches); hash != "" {
		entry = entry.Set("hash", hash)
	}
	return entry
}

func describeSweepMatch(index int, match SweepMatch) string {
	hash, status := match.Hash(), "no vault_test row yet"
	if hash == "" {
		hash = noTxHashYet
	}
	if match.Row != nil {
		status = "vault_test " + match.Row.TxType + " " + match.Row.Status
	}
	return fmt.Sprintf("leg %d %s -> %s: tx hash %s (%s)", index, match.Leg.String("from"), match.Leg.String("to"), hash, status)
}
