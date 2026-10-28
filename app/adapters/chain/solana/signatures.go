package solana

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
)

var (
	_ chain.SignatureReader    = (*SolanaLive)(nil)
	_ chain.AccountStateReader = (*SolanaLive)(nil)
)

// solanaSignaturePageLimit is the most signatures getSignaturesForAddress returns
// per call.
const solanaSignaturePageLimit = 1000

// solanaMintAddressLength is the length of the mint, the first field of a token
// account's data for the Token and the Token-2022 programs alike.
const solanaMintAddressLength = 32

// solanaTokenAmountOffset and solanaTokenAmountLength place a token account's amount
// in its data (after the mint and the owner), for both token programs.
const (
	solanaTokenAmountOffset = 64
	solanaTokenAmountLength = 8
)

// solanaFastestSlot bounds how fast slots are produced, for the floor of a new
// address: mainnet produced 9000 slots in 32 minutes on 2026-10-28 (about 215 ms a
// slot, faster than Agave's 400 ms target), so a quarter of that leaves the floor well
// below the slot an address was created at, also if slots get faster still. A lower
// floor only costs listing an older history, where an account has one.
const solanaFastestSlot = 50 * time.Millisecond

// solanaTransactionNotFoundCode is Agave's answer to a getSignaturesForAddress whose
// before or until it does not hold ("Transaction … not found").
const solanaTransactionNotFoundCode = -32020

// errSolanaTransactionNotReturned means a node answered null for a signature its
// history lists: it does not serve that transaction yet (a lagging backend).
var errSolanaTransactionNotReturned = errors.New("solana: the node returned no transaction for the signature")

// invalidMintRecheck is how long a registered mint the node does not hold as a token
// mint is skipped before it is read again (a registry fix is picked up within it).
const invalidMintRecheck = 10 * time.Minute

// errNotATokenMint means a registered mint is no token mint on the node: no account
// at that address, or one another program owns.
var errNotATokenMint = errors.New("solana: the registered mint is not a token mint on the node")

// mintPrograms caches the token program that owns each registered mint (Token or
// Token-2022), read once with getAccountInfo: a mint never changes program. A mint the
// node does not hold as a token mint is remembered for invalidMintRecheck.
type mintPrograms struct {
	mu       sync.Mutex
	programs map[string]solana.PublicKey
	invalid  map[string]time.Time
}

func newMintPrograms() *mintPrograms {
	return &mintPrograms{programs: map[string]solana.PublicKey{}, invalid: map[string]time.Time{}}
}

func (m *mintPrograms) get(mint string, now time.Time) (solana.PublicKey, bool, bool) {
	if m == nil {
		return solana.PublicKey{}, false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if program, ok := m.programs[mint]; ok {
		return program, true, false
	}
	if until, ok := m.invalid[mint]; ok && now.Before(until) {
		return solana.PublicKey{}, false, true
	}
	return solana.PublicKey{}, false, false
}

func (m *mintPrograms) put(mint string, program solana.PublicKey) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.programs[mint] = program
	delete(m.invalid, mint)
}

func (m *mintPrograms) refuse(mint string, until time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalid[mint] = until
}

// SignatureAccounts returns owner, its associated token account of each registered
// mint (derived with the mint's own token program), and every token account of a
// registered mint that getTokenAccountsByOwner lists for the token programs those
// mints use (a token account that is not the associated one), without repeats. A
// registered mint the node does not hold as a token mint is skipped and named, so
// one bad registry row never stops the other mints or the owners; a read that fails
// is an error.
func (a *SolanaLive) SignatureAccounts(ctx context.Context, owner string) (chain.SignatureAccounts, error) {
	ownerKey, err := a.ownerKey(owner)
	if err != nil {
		return chain.SignatureAccounts{}, err
	}
	derived, err := a.derivedAccounts(ctx, ownerKey)
	if err != nil {
		return chain.SignatureAccounts{}, err
	}
	listed := chain.SignatureAccounts{Accounts: derived.accounts, SkippedMints: derived.skipped}
	seen := make(map[string]bool, len(listed.Accounts))
	for _, account := range listed.Accounts {
		seen[account] = true
	}
	for _, program := range derived.programs {
		held, err := a.tokenAccountsByOwner(ctx, ownerKey, program)
		if err != nil {
			return chain.SignatureAccounts{}, err
		}
		for _, account := range held {
			if _, registered := a.tokens[account.mint]; registered && !seen[account.pubkey] {
				seen[account.pubkey] = true
				listed.Accounts = append(listed.Accounts, account.pubkey)
			}
		}
	}
	return listed, nil
}

// DerivedAccounts returns owner and its associated token account of each registered
// mint the node holds as a token mint: SignatureAccounts without its listing, so no
// call once the mints' programs are cached.
func (a *SolanaLive) DerivedAccounts(ctx context.Context, owner string) ([]string, error) {
	ownerKey, err := a.ownerKey(owner)
	if err != nil {
		return nil, err
	}
	derived, err := a.derivedAccounts(ctx, ownerKey)
	if err != nil {
		return nil, err
	}
	return derived.accounts, nil
}

func (a *SolanaLive) ownerKey(owner string) (solana.PublicKey, error) {
	if a.tokensErr != nil {
		return solana.PublicKey{}, a.tokensErr
	}
	key, err := solana.PublicKeyFromBase58(strings.TrimSpace(owner))
	if err != nil {
		return solana.PublicKey{}, chain.InvalidAddress(fmt.Errorf("sol owner %q: %w", owner, err))
	}
	return key, nil
}

// ownerDerivation is owner and its associated token accounts, the registered mints
// skipped (the node does not hold them as token mints), and the token programs the
// other mints use, in order.
type ownerDerivation struct {
	accounts []string
	skipped  []string
	programs []solana.PublicKey
}

func (a *SolanaLive) derivedAccounts(ctx context.Context, owner solana.PublicKey) (ownerDerivation, error) {
	derived := ownerDerivation{accounts: []string{owner.String()}}
	mints := make([]string, 0, len(a.tokens))
	for mint := range a.tokens {
		mints = append(mints, mint)
	}
	sort.Strings(mints)
	inUse := map[solana.PublicKey]bool{}
	for _, mint := range mints {
		mintKey, err := solana.PublicKeyFromBase58(mint)
		if err != nil {
			return ownerDerivation{}, fmt.Errorf("sol token mint %s: %w", mint, err)
		}
		program, err := a.mintProgram(ctx, mintKey)
		if errors.Is(err, errNotATokenMint) {
			derived.skipped = append(derived.skipped, mint)
			continue
		}
		if err != nil {
			return ownerDerivation{}, err
		}
		inUse[program] = true
		ata, err := associatedTokenAccount(owner, mintKey, program)
		if err != nil {
			return ownerDerivation{}, err
		}
		if account := ata.String(); !slices.Contains(derived.accounts, account) {
			derived.accounts = append(derived.accounts, account)
		}
	}
	for program := range inUse {
		derived.programs = append(derived.programs, program)
	}
	sort.Slice(derived.programs, func(i, j int) bool { return derived.programs[i].String() < derived.programs[j].String() })
	return derived, nil
}

// AccountStates reads accounts with getMultipleAccounts (finalized, chain.AccountStateBatch
// a call): the state of an account is its lamports, its owner program and, for a token
// account, its amount (the 8 bytes at offset 64 of the Token and Token-2022 layouts),
// so a transfer of SOL or of a token changes it; an account that does not exist has
// none. The slot is the lowest the calls were answered at.
func (a *SolanaLive) AccountStates(ctx context.Context, accounts []string) (chain.AccountStates, error) {
	read := chain.AccountStates{States: make(map[string]string, len(accounts))}
	for batch := range slices.Chunk(accounts, chain.AccountStateBatch) {
		for _, account := range batch {
			if _, err := solana.PublicKeyFromBase58(account); err != nil {
				return chain.AccountStates{}, chain.InvalidAddress(fmt.Errorf("sol account %q: %w", account, err))
			}
		}
		var result struct {
			Context struct {
				Slot uint64 `json:"slot"`
			} `json:"context"`
			Value []*struct {
				Lamports uint64   `json:"lamports"`
				Owner    string   `json:"owner"`
				Data     []string `json:"data"`
			} `json:"value"`
		}
		if err := a.rpc.Call(ctx, "getMultipleAccounts", &result, batch, map[string]any{
			"encoding":   "base64",
			"commitment": solanaCommitmentFinalized,
			"dataSlice":  map[string]int{"offset": solanaTokenAmountOffset, "length": solanaTokenAmountLength},
		}); err != nil {
			return chain.AccountStates{}, fmt.Errorf("sol account states: %w", err)
		}
		if len(result.Value) != len(batch) || result.Context.Slot == 0 {
			return chain.AccountStates{}, fmt.Errorf("sol account states: %d answers at slot %d for %d accounts", len(result.Value), result.Context.Slot, len(batch))
		}
		if read.Slot == 0 || result.Context.Slot < read.Slot {
			read.Slot = result.Context.Slot
		}
		for i, account := range result.Value {
			if account == nil {
				read.States[batch[i]] = ""
				continue
			}
			data := ""
			if len(account.Data) > 0 {
				data = account.Data[0]
			}
			read.States[batch[i]] = fmt.Sprintf("%d/%s/%s", account.Lamports, account.Owner, data)
		}
	}
	return read, nil
}

// SlotsWithin is the most slots produced in d, at one every solanaFastestSlot.
func (a *SolanaLive) SlotsWithin(d time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	return uint64(d/solanaFastestSlot) + 1 //nolint:gosec // G115: d is positive here
}

// associatedTokenAccount is owner's associated token account of mint under the token
// program that owns the mint: the seeds are owner, program and mint, so a Token-2022
// mint's account differs from the one solana.FindAssociatedTokenAddress (Token only)
// derives.
func associatedTokenAccount(owner, mint, program solana.PublicKey) (solana.PublicKey, error) {
	address, _, err := solana.FindProgramAddress([][]byte{owner[:], program[:], mint[:]}, solana.SPLAssociatedTokenAccountProgramID)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("sol associated token account of mint %s: %w", mint, err)
	}
	return address, nil
}

// mintProgram is the token program that owns mint (Token or Token-2022), read once
// with getAccountInfo and then cached. A mint the node does not hold, or one another
// program owns, is errNotATokenMint (also cached, for invalidMintRecheck); a read that
// fails is an error and is tried again on the next call.
func (a *SolanaLive) mintProgram(ctx context.Context, mint solana.PublicKey) (solana.PublicKey, error) {
	now := time.Now()
	if program, ok, refused := a.mints.get(mint.String(), now); ok {
		return program, nil
	} else if refused {
		return solana.PublicKey{}, fmt.Errorf("sol mint %s: %w", mint, errNotATokenMint)
	}
	var info struct {
		Value *struct {
			Owner string `json:"owner"`
		} `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getAccountInfo", &info, mint.String(), map[string]any{
		"encoding":   "base64",
		"commitment": solanaCommitmentFinalized,
		"dataSlice":  map[string]int{"offset": 0, "length": 0},
	}); err != nil {
		return solana.PublicKey{}, fmt.Errorf("sol mint %s account: %w", mint, err)
	}
	if info.Value == nil {
		a.mints.refuse(mint.String(), now.Add(invalidMintRecheck))
		return solana.PublicKey{}, fmt.Errorf("sol mint %s: no such account on the node: %w", mint, errNotATokenMint)
	}
	program, err := solana.PublicKeyFromBase58(info.Value.Owner)
	if err != nil || (!program.Equals(solana.TokenProgramID) && !program.Equals(solana.Token2022ProgramID)) {
		a.mints.refuse(mint.String(), now.Add(invalidMintRecheck))
		return solana.PublicKey{}, fmt.Errorf("sol mint %s is owned by %q, not a token program: %w", mint, info.Value.Owner, errNotATokenMint)
	}
	a.mints.put(mint.String(), program)
	return program, nil
}

// heldTokenAccount is one token account getTokenAccountsByOwner listed and its mint.
type heldTokenAccount struct {
	pubkey string
	mint   string
}

// tokenAccountsByOwner lists owner's token accounts under program with their mint,
// reading only the mint (the first 32 bytes of the account data).
func (a *SolanaLive) tokenAccountsByOwner(ctx context.Context, owner, program solana.PublicKey) ([]heldTokenAccount, error) {
	var result struct {
		Value []struct {
			Pubkey  string `json:"pubkey"`
			Account struct {
				Data []string `json:"data"`
			} `json:"account"`
		} `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getTokenAccountsByOwner", &result, owner.String(),
		map[string]string{"programId": program.String()},
		map[string]any{
			"encoding":   "base64",
			"commitment": solanaCommitmentFinalized,
			"dataSlice":  map[string]int{"offset": 0, "length": solanaMintAddressLength},
		}); err != nil {
		return nil, fmt.Errorf("sol token accounts of %s under %s: %w", owner, program, err)
	}
	held := make([]heldTokenAccount, 0, len(result.Value))
	for _, entry := range result.Value {
		if len(entry.Account.Data) == 0 {
			return nil, fmt.Errorf("sol token account %s of %s: no data", entry.Pubkey, owner)
		}
		raw, err := base64.StdEncoding.DecodeString(entry.Account.Data[0])
		if err != nil || len(raw) != solanaMintAddressLength {
			return nil, fmt.Errorf("sol token account %s of %s: the mint cannot be read from its data", entry.Pubkey, owner)
		}
		if _, err := solana.PublicKeyFromBase58(entry.Pubkey); err != nil {
			return nil, fmt.Errorf("sol token account %q of %s: %w", entry.Pubkey, owner, err)
		}
		held = append(held, heldTokenAccount{pubkey: entry.Pubkey, mint: solana.PublicKeyFromBytes(raw).String()})
	}
	return held, nil
}

// solanaSignatureInfo is one entry of getSignaturesForAddress.
type solanaSignatureInfo struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	Err       any    `json:"err"`
}

// AccountSignatures pages getSignaturesForAddress (finalized, newest first, 1000 per
// page) from below query.Before (from the newest signature when empty) down to
// query.Until, the floor, or the end of the history, at most query.MaxPages pages, and
// returns what it read newest first. Every call of one listing goes to one endpoint
// (rpc.PinEndpoint): its finalized slot first (Head), then the pages, then the
// evidence of the end. A page shorter than the limit ends a listing with Until only
// once the endpoint confirms it: one more call for the single signature below the
// oldest it listed (or below Before) must answer Until itself. Agave fails a listing
// whose until or before it does not hold, but another provider may answer an empty
// or short page instead, which must never read as reaching the cursor. An empty first
// page without Before is confirmed from the newest signature, so one that landed
// between the page and the confirmation answers instead of Until (a busy account):
// the listing is then read again once, on the same endpoint, and its new short page
// confirmed the same way; a second miss leaves the listing unended. A listing with
// Before reads its first page from right below query.Anchor (from the newest when
// empty), and that page must start with Before itself, which is left out: only an
// address index that holds Before right below the anchor passes, with no call of its
// own. One that does not hold it (a backend behind on the newest slots, or without
// the old ledger) may answer an empty page, its own newest signatures or, as Agave
// does, -32020; whatever it answers, nothing it would list or say of the end below
// Before is read, and the listing comes back empty and unended. Without Until
// only an empty page ends a listing, and then the endpoint's first available block
// (getFirstAvailableBlock) is LedgerStart: a node that does not hold an old ledger
// answers short pages with no error, which must never read as the start of the
// history.
func (a *SolanaLive) AccountSignatures(ctx context.Context, account string, query chain.SignatureQuery) (chain.SignatureHistory, error) {
	var history chain.SignatureHistory
	if _, err := solana.PublicKeyFromBase58(strings.TrimSpace(account)); err != nil {
		return history, chain.InvalidAddress(fmt.Errorf("sol account %q: %w", account, err))
	}
	if query.MaxPages < 1 {
		return history, fmt.Errorf("sol signatures of %s: at least one page must be allowed", account)
	}
	pinned := rpc.PinEndpoint(ctx)
	if err := a.rpc.Call(pinned, "getSlot", &history.Head, map[string]string{"commitment": solanaCommitmentFinalized}); err != nil {
		return chain.SignatureHistory{}, fmt.Errorf("sol signatures of %s: head: %w", account, err)
	}
	before, placing := query.Before, query.Before != ""
	if placing {
		before = query.Anchor
	}
	pages, relisted := 0, false
	for {
		if pages == query.MaxPages {
			history.More = true
			break
		}
		options := map[string]any{"commitment": solanaCommitmentFinalized, "limit": solanaSignaturePageLimit}
		if query.Until != "" {
			options["until"] = query.Until
		}
		if before != "" {
			options["before"] = before
		}
		var entries []solanaSignatureInfo
		if err := a.rpc.Call(pinned, "getSignaturesForAddress", &entries, account, options); err != nil {
			if placing && transactionNotFound(err) {
				return chain.SignatureHistory{Head: history.Head}, nil
			}
			return chain.SignatureHistory{}, fmt.Errorf("sol signatures of %s: %w", account, err)
		}
		page := entries
		if placing {
			placing = false
			if len(entries) == 0 || entries[0].Signature != query.Before {
				return chain.SignatureHistory{Head: history.Head}, nil
			}
			page = entries[1:]
		}
		for _, entry := range page {
			if strings.TrimSpace(entry.Signature) == "" {
				return chain.SignatureHistory{}, fmt.Errorf("sol signatures of %s: an entry has no signature", account)
			}
			if query.HasFloor && entry.Slot < query.FloorSlot {
				history.Ended, history.FloorCrossed = true, true
				return history, nil
			}
			history.Signatures = append(history.Signatures, chain.AccountSignature{Signature: entry.Signature, Slot: entry.Slot, Failed: entry.Err != nil})
		}
		if query.Until != "" && len(entries) < solanaSignaturePageLimit {
			below := before
			if len(entries) > 0 {
				below = entries[len(entries)-1].Signature
			}
			next, err := a.signatureBelow(pinned, account, below)
			if err != nil {
				return chain.SignatureHistory{}, fmt.Errorf("sol signatures of %s: confirm the cursor: %w", account, err)
			}
			if next != query.Until && next != "" && below == "" && !relisted {
				// The page was empty and the newest signature now is another one: it
				// landed after the page. The listing starts again from the newest, which
				// lists it, and is confirmed below it; this empty page read nothing and
				// does not count as one.
				relisted = true
				continue
			}
			history.Ended = next == query.Until
			return history, nil
		}
		if len(page) == 0 {
			if err := a.rpc.Call(pinned, "getFirstAvailableBlock", &history.LedgerStart); err != nil {
				return chain.SignatureHistory{}, fmt.Errorf("sol signatures of %s: first available block: %w", account, err)
			}
			history.Ended = true
			return history, nil
		}
		before = entries[len(entries)-1].Signature
		pages++
	}
	return history, nil
}

// transactionNotFound reports whether err is the endpoint's -32020: it does not hold
// a signature the listing named.
func transactionNotFound(err error) bool {
	var rpcErr *rpc.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == solanaTransactionNotFoundCode
}

// signatureBelow is the signature right below below (the newest one when below is
// empty) on the listing's endpoint (ctx pinned), "" when the endpoint lists none: a
// short page reached the cursor only when it is the cursor itself.
func (a *SolanaLive) signatureBelow(ctx context.Context, account, below string) (string, error) {
	options := map[string]any{"commitment": solanaCommitmentFinalized, "limit": 1}
	if below != "" {
		options["before"] = below
	}
	var entries []solanaSignatureInfo
	if err := a.rpc.Call(ctx, "getSignaturesForAddress", &entries, account, options); err != nil {
		return "", err
	}
	if len(entries) != 1 {
		return "", nil
	}
	return entries[0].Signature, nil
}

// solanaSignedTransaction is getTransaction with the json encoding: the static
// account keys in the message, and the keys a version-0 or later transaction loaded
// from address lookup tables in meta.loadedAddresses (writable, then readonly).
type solanaSignedTransaction struct {
	Slot        uint64 `json:"slot"`
	BlockTime   *int64 `json:"blockTime"`
	Transaction struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			AccountKeys []string `json:"accountKeys"`
		} `json:"message"`
	} `json:"transaction"`
	Meta *struct {
		solanaTransactionMeta
		LoadedAddresses *struct {
			Writable []string `json:"writable"`
			Readonly []string `json:"readonly"`
		} `json:"loadedAddresses"`
	} `json:"meta"`
}

// balanceKeys is the transaction's account keys in the order of its balances, the
// order getBlock's "accounts" details list them in: the static keys, then the
// lookup-table loaded writable keys, then the readonly ones.
func (tx *solanaSignedTransaction) balanceKeys() []string {
	keys := append([]string(nil), tx.Transaction.Message.AccountKeys...)
	if tx.Meta != nil && tx.Meta.LoadedAddresses != nil {
		keys = append(keys, tx.Meta.LoadedAddresses.Writable...)
		keys = append(keys, tx.Meta.LoadedAddresses.Readonly...)
	}
	return keys
}

// TransactionTransfers reads one finalized transaction (getTransaction, json, the
// newest version the adapter reads) and returns its credits as ScanBlock finds them
// in its block (solanaTransactionCredits), with its slot and block time and without a
// block hash. A transaction the node does not return, one that is not the signature
// asked for, or one whose keys do not line up with its balances (an answer without
// its loaded addresses) is an error, never a transaction without credits.
func (a *SolanaLive) TransactionTransfers(ctx context.Context, signature string) (chain.TransactionTransfers, error) {
	if a.tokensErr != nil {
		return chain.TransactionTransfers{}, a.tokensErr
	}
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return chain.TransactionTransfers{}, fmt.Errorf("sol transaction signature is required")
	}
	var tx *solanaSignedTransaction
	if err := a.rpc.Call(ctx, "getTransaction", &tx, signature, map[string]any{
		"encoding": "json", "commitment": solanaCommitmentFinalized, "maxSupportedTransactionVersion": solanaMaxSupportedTxVersion,
	}); err != nil {
		return chain.TransactionTransfers{}, fmt.Errorf("sol transaction %s: %w", signature, err)
	}
	if tx == nil {
		return chain.TransactionTransfers{}, fmt.Errorf("sol transaction %s: %w", signature, errSolanaTransactionNotReturned)
	}
	if len(tx.Transaction.Signatures) == 0 || tx.Transaction.Signatures[0] != signature {
		return chain.TransactionTransfers{}, fmt.Errorf("sol transaction %s: the node answered another transaction", signature)
	}
	if tx.Meta == nil {
		return chain.TransactionTransfers{}, fmt.Errorf("sol transaction %s: the node returned it without its meta", signature)
	}
	keys := tx.balanceKeys()
	if len(tx.Meta.PreBalances) != len(keys) || len(tx.Meta.PostBalances) != len(keys) {
		return chain.TransactionTransfers{}, fmt.Errorf("sol transaction %s: %d account keys for %d balances", signature, len(keys), len(tx.Meta.PreBalances))
	}
	meta := tx.Meta.solanaTransactionMeta
	transfers, err := solanaTransactionCredits(solanaSlotOf(tx.Slot, "", tx.BlockTime),
		solanaBalanceTransaction{Signature: signature, Keys: keys, Meta: &meta}, a.cfg.NativeSymbol, a.tokens)
	if err != nil {
		return chain.TransactionTransfers{}, err
	}
	return chain.TransactionTransfers{Slot: tx.Slot, Transfers: transfers}, nil
}
