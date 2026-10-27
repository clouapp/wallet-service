// Package withdrawrecoveryruntime supplies explicit, read-only runtime evidence
// and an exact-byte sender. It has no container, signing or persistence access.
package withdrawrecoveryruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/hdkey"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

type Attempts interface {
	FindByOperation(context.Context, uuid.UUID) (*models.WithdrawalRecoveryAttempt, error)
}
type Withdrawals interface {
	FindByID(context.Context, uuid.UUID) (*models.Withdrawal, error)
}
type Transactions interface {
	FindByID(context.Context, uuid.UUID) (*models.Transaction, error)
}
type Wallets interface {
	FindByID(context.Context, uuid.UUID) (*models.Wallet, error)
}
type Addresses interface {
	FindByID(context.Context, uuid.UUID) (*models.Address, error)
}
type Accounts interface {
	FindByID(context.Context, uuid.UUID) (*models.Account, error)
}
type Chains interface {
	FindByID(context.Context, string) (*models.Chain, error)
}
type Tokens interface {
	FindByChainID(context.Context, string) ([]models.Token, error)
}
type Registry interface {
	ChainForWallet(*models.Wallet) (types.Chain, error)
	FindToken(string, string) (*types.Token, error)
}

// RPC matches the existing RPC client's narrow call seam. Each endpoint must be
// a dedicated single-provider client: hidden overlapping failovers are forbidden.
type RPC interface {
	Call(context.Context, string, interface{}, ...interface{}) error
}
type Endpoint struct {
	Origin string
	Client RPC
}
type Dependencies struct {
	Attempts       Attempts
	Withdrawals    Withdrawals
	Transactions   Transactions
	Wallets        Wallets
	Addresses      Addresses
	Accounts       Accounts
	Chains         Chains
	Tokens         Tokens
	Registry       Registry
	Endpoints      [2]Endpoint
	ProviderOrigin string
	CodeRevision   string
	Now            func() time.Time
}
type Runtime struct{ deps Dependencies }

func refuse(reason string) error { return fmt.Errorf("%w: %s", recovery.ErrRefused, reason) }
func New(deps Dependencies) (*Runtime, error) {
	if deps.Withdrawals == nil || deps.Transactions == nil || deps.Wallets == nil || deps.Addresses == nil || deps.Accounts == nil || deps.Chains == nil || deps.Tokens == nil || deps.Registry == nil {
		return nil, refuse("all read repositories and registry are required")
	}
	origin, err := originOf(deps.ProviderOrigin)
	if err != nil || deps.CodeRevision == "" || strings.TrimSpace(deps.CodeRevision) != deps.CodeRevision {
		return nil, refuse("trusted provider origin and code revision are required")
	}
	deps.ProviderOrigin = origin
	for i := range deps.Endpoints {
		e := &deps.Endpoints[i]
		if e.Client == nil {
			return nil, refuse("two independent RPC clients are required")
		}
		e.Origin, err = originOf(e.Origin)
		if err != nil {
			return nil, refuse("RPC origins must be non-secret HTTPS origins")
		}
	}
	a, _ := url.Parse(deps.Endpoints[0].Origin)
	z, _ := url.Parse(deps.Endpoints[1].Origin)
	first, second := reflect.ValueOf(deps.Endpoints[0].Client), reflect.ValueOf(deps.Endpoints[1].Client)
	sameClient := first.Type() == second.Type() && first.Comparable() && first.Interface() == second.Interface()
	if strings.EqualFold(a.Hostname(), z.Hostname()) || sameClient {
		return nil, refuse("RPC providers must be independent")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Runtime{deps: deps}, nil
}
func originOf(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", refuse("invalid origin")
	}
	return "https://" + strings.ToLower(u.Host), nil
}
func positive(value string) (*big.Int, error) {
	n, ok := amount.ParseBaseUnits(value)
	if !ok || n.Sign() <= 0 || n.BitLen() > 256 {
		return nil, refuse("invalid exact amount")
	}
	return n, nil
}
func sameAddress(a, b string) bool {
	return common.IsHexAddress(a) && common.IsHexAddress(b) && common.HexToAddress(a) == common.HexToAddress(b)
}
func digest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type records struct {
	withdrawal    *models.Withdrawal
	original      *models.Transaction
	wallet        *models.Wallet
	source        *models.Address
	account       *models.Account
	network       *models.Chain
	token         *types.Token
	adapter       types.Chain
	binding       recovery.Binding
	candidateHash string
	candidate     *models.Transaction
}

// ReadBinding discovers the canonical server-side request hash. Only the two
// durable IDs select it; the caller supplies no economic hash or mutable source.
func (r *Runtime) ReadBinding(ctx context.Context, operationID, originalID uuid.UUID) (recovery.Binding, error) {
	rec, err := r.read(ctx, operationID, originalID)
	if err != nil {
		return recovery.Binding{}, err
	}
	return rec.binding, nil
}
func (r *Runtime) read(ctx context.Context, op, originalID uuid.UUID) (*records, error) {
	if ctx == nil || op == uuid.Nil || originalID == uuid.Nil {
		return nil, refuse("exact durable operation and original transaction IDs are required")
	}
	w, err := r.deps.Withdrawals.FindByID(ctx, op)
	if err != nil || w == nil || w.ID != op || w.AccountID == nil || w.TransactionID == nil || w.DestinationTag != nil {
		return nil, refuse("withdrawal binding unavailable")
	}
	tx, err := r.deps.Transactions.FindByID(ctx, originalID)
	if err != nil || tx == nil || tx.ID != originalID || tx.WalletID != w.WalletID || tx.AddressID == nil || tx.TxType != models.TxTypeWithdrawal || tx.DestinationTag != nil || tx.Status != "failed" || tx.ErrorMessage != "broadcast_refused" || tx.BlockNumber != 0 || tx.ConfirmedAt != nil {
		return nil, refuse("original refused transaction unavailable")
	}
	if !sameAddress(tx.ToAddress, w.DestinationAddress) {
		return nil, refuse("withdrawal recipient changed")
	}
	wallet, err := r.deps.Wallets.FindByID(ctx, w.WalletID)
	if err != nil || wallet == nil || wallet.ID != w.WalletID || wallet.AccountID == nil || *wallet.AccountID != *w.AccountID || wallet.Chain != tx.Chain || wallet.Status != "active" || wallet.FrozenUntil != nil {
		return nil, refuse("wallet account, status or chain mismatch")
	}
	account, err := r.deps.Accounts.FindByID(ctx, *w.AccountID)
	if err != nil || account == nil || account.ID != *w.AccountID || account.Environment != models.EnvironmentTest || account.Status != models.StatusActive {
		return nil, refuse("active test account required")
	}
	network, err := r.deps.Chains.FindByID(ctx, tx.Chain)
	if err != nil || network == nil || network.ID != tx.Chain || network.Status != models.StatusActive || network.AdapterType != models.AdapterTypeEVM || !network.IsTestnet || network.NetworkID == nil || *network.NetworkID <= 0 || network.RequiredConfirmations <= 0 {
		return nil, refuse("active EVM test chain required")
	}
	source, err := r.deps.Addresses.FindByID(ctx, *tx.AddressID)
	if err != nil || source == nil || source.ID != *tx.AddressID || source.WalletID != wallet.ID || source.Chain != tx.Chain || !source.IsActive || source.DerivationType != "bip32" || source.DerivationIndex < 1 || source.DerivationIndex > math.MaxInt32 || source.ExternalUserID == "" || source.ExternalUserID == "system" || !sameAddress(source.Address, tx.FromAddress) {
		return nil, refuse("active owned non-hardened child required")
	}
	public, err := hex.DecodeString(wallet.MPCPublicKey)
	if err != nil {
		return nil, refuse("wallet public derivation unavailable")
	}
	code, err := hex.DecodeString(wallet.MPCChainCode)
	if err != nil {
		return nil, refuse("wallet public derivation unavailable")
	}
	child, err := hdkey.DeriveSecp256k1Child(public, code, uint32(source.DerivationIndex))
	if err != nil {
		return nil, refuse("child public derivation failed")
	}
	key, err := crypto.DecompressPubkey(child.PublicKey)
	if err != nil || !sameAddress(crypto.PubkeyToAddress(*key).Hex(), source.Address) {
		return nil, refuse("child address does not match public derivation")
	}
	tokens, err := r.deps.Tokens.FindByChainID(ctx, tx.Chain)
	if err != nil {
		return nil, refuse("token registry unavailable")
	}
	var matched []models.Token
	for _, t := range tokens {
		if t.Status == models.TokenStatusActive && t.Symbol == tx.Asset {
			matched = append(matched, t)
		}
	}
	if len(matched) != 1 || matched[0].ChainID != tx.Chain || !sameAddress(matched[0].ContractAddress, tx.TokenContract) || matched[0].Decimals < 0 || matched[0].Decimals > 36 {
		return nil, refuse("unique active token contract and precision required")
	}
	adapter, err := r.deps.Registry.ChainForWallet(wallet)
	if err != nil || adapter == nil || adapter.ID() != network.ID {
		return nil, refuse("wallet-scoped adapter unavailable")
	}
	token, err := r.deps.Registry.FindToken(tx.Chain, tx.Asset)
	if err != nil || token == nil || token.Symbol != tx.Asset || token.ChainID != tx.Chain || !sameAddress(token.Contract, matched[0].ContractAddress) || int(token.Decimals) != matched[0].Decimals || types.SameAssetSymbol(tx.Asset, adapter.NativeAsset()) {
		return nil, refuse("runtime token registry differs")
	}
	units, err := positive(tx.Amount)
	if err != nil {
		return nil, err
	}
	human, err := amount.DecimalToBaseUnits(amount.NormalizeDecimal(w.Amount), matched[0].Decimals)
	if err != nil || human.Cmp(units) != 0 {
		return nil, refuse("withdrawal and original raw amounts differ")
	}
	var payload struct {
		Outcome string  `json:"broadcast_outcome"`
		Nonce   *uint64 `json:"sender_nonce"`
	}
	if json.Unmarshal([]byte(tx.RawPayload), &payload) != nil || payload.Outcome != chain.BroadcastOutcomeRefused || payload.Nonce == nil {
		return nil, refuse("original nonce and refused outcome required")
	}
	b := recovery.Binding{OperationID: op, OriginalTransactionID: originalID, OriginalTxHash: strings.ToLower(tx.TxHash), AccountID: account.ID, WalletID: wallet.ID, SourceAddressID: source.ID, SourceAddress: strings.ToLower(source.Address), SourceDerivationIndex: source.DerivationIndex, SourceExternalUserID: source.ExternalUserID, Chain: network.ID, NetworkID: *network.NetworkID, Nonce: payload.Nonce, Asset: tx.Asset, TokenContract: strings.ToLower(token.Contract), TokenDecimals: matched[0].Decimals, AmountRaw: units.String(), Recipient: strings.ToLower(tx.ToAddress), RequiredConfirmations: tx.RequiredConfs, Finality: tx.FinalityPolicy()}
	if b.RequiredConfirmations != network.RequiredConfirmations || b.Finality != network.FinalityPolicy() {
		return nil, refuse("original and current finality policy differ")
	}
	b.ProviderFingerprint = digest(struct{ Origin, Revision, Account, Wallet, Label string }{r.deps.ProviderOrigin, r.deps.CodeRevision, account.ID.String(), wallet.ID.String(), wallet.Label})
	economic := b
	economic.RequestHash = ""
	economic.ProviderFingerprint = ""
	b.RequestHash = digest(economic)
	if err := b.Validate(); err != nil {
		return nil, err
	}
	candidateHash := ""
	var linkedCandidate *models.Transaction
	if *w.TransactionID != originalID {
		candidate, err := r.deps.Transactions.FindByID(ctx, *w.TransactionID)
		if err != nil || candidate == nil || candidate.ID != *w.TransactionID || candidate.TxType != models.TxTypeWithdrawal || candidate.IdempotencyKey == nil || *candidate.IdempotencyKey != op.String() || candidate.ParentTransactionID == nil || *candidate.ParentTransactionID != originalID || candidate.WalletID != wallet.ID || candidate.AddressID == nil || *candidate.AddressID != source.ID || candidate.Chain != b.Chain || candidate.ExternalUserID != b.SourceExternalUserID || candidate.RequiredConfs != b.RequiredConfirmations || candidate.FinalityPolicy() != b.Finality || candidate.Amount != b.AmountRaw || candidate.Asset != b.Asset || !sameAddress(candidate.FromAddress, b.SourceAddress) || !sameAddress(candidate.ToAddress, b.Recipient) || !sameAddress(candidate.TokenContract, b.TokenContract) {
			return nil, refuse("current candidate has no original economic lineage")
		}
		var lineage struct {
			Operation   string  `json:"recovery_operation_id"`
			Previous    string  `json:"recovery_predecessor_transaction_id"`
			Nonce       *uint64 `json:"sender_nonce"`
			BindingHash string  `json:"recovery_binding_hash"`
		}
		if json.Unmarshal([]byte(candidate.RawPayload), &lineage) != nil || lineage.Operation != op.String() || lineage.Previous != originalID.String() || lineage.Nonce == nil || *lineage.Nonce != *b.Nonce || lineage.BindingHash != digest(b) {
			return nil, refuse("candidate operation lineage mismatch")
		}
		if !hashPattern.MatchString(candidate.TxHash) {
			return nil, refuse("durable candidate hash invalid")
		}
		linkedCandidate = candidate
		candidateHash = strings.ToLower(candidate.TxHash)
	} else if w.Status != models.WithdrawalStatusFailed {
		return nil, refuse("original withdrawal is not failed")
	}
	return &records{withdrawal: w, original: tx, wallet: wallet, source: source, account: account, network: network, token: token, adapter: adapter, binding: b, candidateHash: candidateHash, candidate: linkedCandidate}, nil
}

func (r *Runtime) Observe(ctx context.Context, expected recovery.Binding, candidateHash string) (*recovery.Observation, error) {
	rec, err := r.read(ctx, expected.OperationID, expected.OriginalTransactionID)
	if err != nil {
		return nil, err
	}
	if digest(rec.binding) != digest(expected) {
		return nil, refuse("server-derived binding differs from caller")
	}
	return r.observe(ctx, rec, candidateHash)
}
func (r *Runtime) observe(ctx context.Context, rec *records, candidate string) (*recovery.Observation, error) {
	started := r.deps.Now()
	b := rec.binding
	if rec.candidateHash != "" {
		if candidate != "" && !strings.EqualFold(candidate, rec.candidateHash) {
			return nil, refuse("candidate differs from current durable link")
		}
		candidate = rec.candidateHash
	}
	if candidate != "" && !hashPattern.MatchString(candidate) {
		return nil, refuse("candidate hash invalid")
	}
	var facts [2]rpcFacts
	for i, e := range r.deps.Endpoints {
		f, err := readRPC(ctx, e.Client, b, candidate)
		if err != nil {
			return nil, err
		}
		facts[i] = f
	}
	a, z := facts[0], facts[1]
	agree := a.network == z.network && a.latest == z.latest && a.pending == z.pending && a.native == z.native && a.tokens == z.tokens && a.decimals == z.decimals && a.originalKnown == z.originalKnown && a.candidateKnown == z.candidateKnown && a.receipt == z.receipt
	if a.network != b.NetworkID || z.network != b.NetworkID || a.decimals != b.TokenDecimals || z.decimals != b.TokenDecimals {
		return nil, refuse("RPC network or token precision differs from registry")
	}
	var quote recovery.FeeQuote
	var err error
	// A known transfer or a consumed nonce is reconciliation evidence first; a
	// now-insufficient token balance must not hide it behind a quote failure.
	if !a.originalKnown && !z.originalKnown && !a.candidateKnown && !z.candidateKnown && !a.receipt && !z.receipt && a.latest == *b.Nonce && z.latest == *b.Nonce && a.pending == *b.Nonce && z.pending == *b.Nonce {
		quote, err = quoteFee(ctx, rec.adapter, b, rec.token)
		if err != nil {
			return nil, err
		}
	}
	// A long or changing observation cannot turn several independent reads into a
	// fictional atomic snapshot. The domain also enforces freshness before action.
	if r.deps.Now().Sub(started) > recovery.MaxObservationAge {
		return nil, refuse("runtime observation exceeded freshness window")
	}
	again, err := r.read(ctx, b.OperationID, b.OriginalTransactionID)
	if err != nil || digest(again.binding) != digest(b) {
		return nil, refuse("provider binding changed during observation")
	}
	return &recovery.Observation{Binding: b, ObservedAt: started, AccountEnvironment: rec.account.Environment, WalletStatus: rec.wallet.Status, WalletFrozen: rec.wallet.FrozenUntil != nil, ChainTestnet: rec.network.IsTestnet, AdapterType: rec.network.AdapterType, RPCNetworkID: a.network, RPCsAgree: agree, OriginalStatus: rec.original.Status, BroadcastOutcome: chain.BroadcastOutcomeRefused, LatestNonce: &a.latest, PendingNonce: &a.pending, OriginalKnown: a.originalKnown || z.originalKnown, CandidateKnown: a.candidateKnown || z.candidateKnown, ReceiptObserved: a.receipt || z.receipt, SourceTokenBalanceRaw: a.tokens, SourceNativeBalanceWei: a.native, Quote: quote}, nil
}
