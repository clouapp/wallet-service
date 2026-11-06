// Package swapcustody funds pinned swap routes using the existing MPC custody.
package swapcustody

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	contractscache "github.com/goravel/framework/contracts/cache"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

type Authority interface {
	AuthorizeSwap(context.Context, *models.Wallet, swap.ExecuteInput) error
	ReserveSwapSpend(context.Context, *models.Wallet, *swap.Quote, swap.ExecuteInput) error
}

type Registry interface {
	ChainForWallet(*models.Wallet) (types.Chain, error)
}
type Steps interface {
	ExecutionSteps(context.Context, uuid.UUID) ([]models.SwapExecutionStep, error)
	RecordSigned(context.Context, *models.SwapExecutionStep, *models.Transaction) error
}
type Transactions interface {
	FindByID(context.Context, uuid.UUID) (*models.Transaction, error)
}

// Tokens is the custody token catalog of a chain. An EVM swap call never targets
// one of these contracts, except the source token's exact deposit transfer.
type Tokens interface {
	TokensForChain(string) []types.Token
}

// Settler settles a signed row whose send was never answered by sending the
// same bytes again (sweep.SettleUnanswered); it never signs.
type Settler interface {
	SettleUnanswered(context.Context, *models.Transaction) (*models.Transaction, error)
}

type Deps struct {
	Authority    Authority
	Registry     Registry
	Steps        Steps
	Transactions Transactions
	Signer       sweep.PreparedSigner
	Cache        contractscache.Driver
	Tokens       Tokens
	Settler      Settler
	// InclusionWait is how long one Send waits, inside the request, for an ERC-20
	// approval to be included before it signs the funding (bounded by the quote's
	// expiry and the request deadline). Zero returns Pending right after the approval.
	InclusionWait time.Duration
	// PollInterval paces the inclusion reads (default 2 s).
	PollInterval time.Duration
}

type Service struct{ deps Deps }

func New(deps Deps) *Service {
	if deps.PollInterval <= 0 {
		deps.PollInterval = 2 * time.Second
	}
	return &Service{deps: deps}
}

func (s *Service) Authorize(ctx context.Context, wallet *models.Wallet, in swap.ExecuteInput) error {
	return s.deps.Authority.AuthorizeSwap(ctx, wallet, in)
}

// advanced is the outcome of one pass over the order's durable steps.
type advanced struct {
	result *swap.SendResult
	err    error
	// await is an approval just broadcast: the next pass funds once it is included.
	await string
	// retry is an included approval whose allowance the node does not show yet.
	retry bool
}

// Send advances the order's signed transactions. Every pass first reads the
// durable steps and never generates another funding transaction. An ERC-20
// approval is waited for inside the request (InclusionWait) so the funding is
// signed with the same authorization before the quote expires.
func (s *Service) Send(ctx context.Context, order *models.SwapOrder, wallet *models.Wallet, quote *swap.Quote, funding *swap.Funding, in swap.ExecuteInput) (*swap.SendResult, error) {
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	if s.deps.Cache == nil || s.deps.Signer == nil {
		return nil, preSend(swap.ErrDisabled)
	}
	var holds []*cacheguard.Hold
	defer func() {
		for i := len(holds) - 1; i >= 0; i-- {
			holds[i].Release()
		}
	}()
	for _, prefix := range []string{"vault:lock:withdrawal:", "vault:lock:wallet_ops:"} {
		hold, acquired, err := cacheguard.AcquireHold(s.deps.Cache, prefix+wallet.ID.String(), time.Minute)
		if err != nil {
			return nil, preSend(err)
		}
		if !acquired {
			return &swap.SendResult{Pending: true}, nil
		}
		holds = append(holds, hold)
	}
	deadline := s.waitDeadline(ctx, quote)
	for {
		pass := s.advance(ctx, holds, order, wallet, quote, funding, in)
		switch {
		case pass.await != "":
			if !s.awaitInclusion(ctx, wallet, pass.await, holds, deadline) {
				return pass.result, pass.err
			}
		case pass.retry:
			if !s.pause(ctx, holds, deadline) {
				return pass.result, pass.err
			}
		default:
			return pass.result, pass.err
		}
	}
}

func (s *Service) advance(ctx context.Context, holds []*cacheguard.Hold, order *models.SwapOrder, wallet *models.Wallet, quote *swap.Quote, funding *swap.Funding, in swap.ExecuteInput) advanced {
	fail := func(err error) advanced { return advanced{err: preSend(err)} }
	pending := func(err error) advanced { return advanced{result: &swap.SendResult{Pending: true}, err: err} }
	steps, err := s.deps.Steps.ExecutionSteps(ctx, order.ID)
	if err != nil {
		return fail(err)
	}
	approvalConfirmed := false
	// Catalog tokens whose standing allowance to the call target is being revoked.
	revoking := map[string]bool{}
	for _, step := range steps {
		if step.Purpose == "funding" {
			return advanced{result: &swap.SendResult{Hash: step.TxHash, Uncertain: true}}
		}
		tx, readErr := s.deps.Transactions.FindByID(ctx, step.TransactionID)
		if readErr != nil {
			return pending(readErr)
		}
		if tx.Status == string(types.TxStatusFailed) {
			return fail(fmt.Errorf("swap approval failed"))
		}
		approvalConfirmed = tx.Status == string(types.TxStatusConfirmed)
		if step.Purpose == "approval_revoke" && !approvalConfirmed {
			revoking[strings.ToLower(tx.ToAddress)] = true
		}
		if !approvalConfirmed {
			// An EVM approval can fund the next nonce once successfully included.
			// The following RPC estimate and allowance read must also succeed; the
			// destination credit still waits for the full confirmation policy.
			if s.deps.Registry == nil {
				return pending(nil)
			}
			approvalChain, chainErr := s.deps.Registry.ChainForWallet(wallet)
			if chainErr != nil {
				return pending(chainErr)
			}
			block, receiptErr := approvalChain.GetTransactionBlock(ctx, step.TxHash)
			if errors.Is(receiptErr, chain.ErrTransactionFailed) {
				return fail(receiptErr)
			}
			if receiptErr != nil || block == 0 {
				return pending(nil)
			}
		}
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return fail(swap.ErrExpired)
	}
	adapter, err := s.deps.Registry.ChainForWallet(wallet)
	if err != nil {
		return fail(err)
	}
	input, ok := amount.ParseBaseUnits(quote.Request.AmountRaw)
	if !ok || input.Sign() <= 0 || wallet.DepositAddress == nil {
		return fail(swap.ErrInvalidRequest)
	}
	// Quotes are made from the base address. Consolidation is a separate operation:
	// silently selecting a child would invalidate sender-bound provider routes.
	var balance *types.Balance
	var token *types.Token
	if quote.Request.From.Native {
		balance, err = adapter.GetBalance(ctx, quote.Request.FromAddress)
	} else {
		token = &types.Token{Symbol: quote.Request.From.Symbol, Contract: quote.Request.From.Contract, Decimals: uint8(quote.Request.From.Decimals), ChainID: wallet.Chain}
		balance, err = adapter.GetTokenBalance(ctx, quote.Request.FromAddress, *token)
	}
	if err != nil {
		return fail(err)
	}
	if balance == nil || balance.Amount == nil || balance.Amount.Cmp(input) < 0 {
		return fail(chain.Insufficient(fmt.Errorf("swap requires funded base address")))
	}
	if len(steps) == 0 {
		if err := s.deps.Authority.ReserveSwapSpend(ctx, wallet, quote, in); err != nil {
			return fail(err)
		}
	}
	purpose := "funding"
	recordTo := funding.To
	var unsigned *types.UnsignedTx
	switch funding.Kind {
	case "evm":
		builder, supported := adapter.(chain.SwapCallBuilder)
		if !supported || funding.ChainID != quote.Request.From.NetworkID || funding.Memo != "" {
			return fail(swap.ErrUnsupported)
		}
		if s.deps.Tokens == nil {
			return fail(swap.ErrDisabled)
		}
		value, valid := parseValue(funding.AmountRaw)
		if !valid || (token == nil && value.Cmp(input) != 0) || (token != nil && value.Sign() != 0) {
			return fail(swap.ErrInvalidQuote)
		}
		data, decodeErr := hex.DecodeString(strings.TrimPrefix(funding.Data, "0x"))
		if decodeErr != nil {
			return fail(swap.ErrInvalidQuote)
		}
		catalog := s.deps.Tokens.TokensForChain(wallet.Chain)
		directTransfer, guardErr := guardEVMCall(quote, funding.To, value, data, token, input, funding.ApprovalAddress, catalog)
		if guardErr == nil && !directTransfer {
			// A target the wallet holds a balance of is a token (catalog or not):
			// no router is one, and a call to it could move that balance.
			if held, heldErr := adapter.GetTokenBalance(ctx, quote.Request.FromAddress, types.Token{Contract: funding.To, ChainID: wallet.Chain}); heldErr == nil && held != nil && held.Amount != nil && held.Amount.Sign() > 0 {
				guardErr = fmt.Errorf("%w: swap call targets a token the wallet holds", swap.ErrInvalidQuote)
			}
		}
		if guardErr != nil {
			slog.Warn("swap funding call refused by the custody guard", "order_id", order.ID, "provider", quote.Provider, "chain", wallet.Chain, "selector", hexSelector(data), "reason", guardErr.Error())
			return fail(guardErr)
		}
		to := funding.To
		revoke := ""
		if !directTransfer {
			// A standing allowance from an earlier, unfinished order would let this
			// call (or the spender at any time) pull more than the quoted input: it
			// is revoked first, one token per step, before anything is funded.
			for _, listed := range catalog {
				if listed.Contract == "" || (token != nil && strings.EqualFold(listed.Contract, token.Contract)) {
					continue
				}
				standing, standingErr := builder.SwapAllowance(ctx, listed.Contract, quote.Request.FromAddress, funding.To)
				if standingErr != nil {
					return fail(standingErr)
				}
				if standing.Sign() == 0 {
					continue
				}
				if revoking[strings.ToLower(listed.Contract)] {
					// Revoked but not visible to this node yet: never revoke twice.
					return advanced{result: &swap.SendResult{Pending: true}, retry: true}
				}
				revoke = listed.Contract
				break
			}
		}
		if revoke != "" {
			purpose = "approval_revoke"
			data, decodeErr = approvalData(funding.To, new(big.Int))
			if decodeErr != nil {
				return fail(decodeErr)
			}
			to, value = revoke, new(big.Int)
		} else if token != nil && !directTransfer {
			if !adapter.ValidateAddress(funding.ApprovalAddress) {
				return fail(swap.ErrInvalidQuote)
			}
			allowance, allowanceErr := builder.SwapAllowance(ctx, token.Contract, quote.Request.FromAddress, funding.ApprovalAddress)
			if allowanceErr != nil {
				return fail(allowanceErr)
			}
			if allowance.Cmp(input) != 0 {
				for _, step := range steps {
					if step.Purpose == "approval" || (step.Purpose == "approval_reset" && allowance.Sign() > 0) {
						if approvalConfirmed {
							return fail(fmt.Errorf("confirmed swap allowance changed before funding"))
						}
						// Included but not yet visible to this node: never approve twice.
						return advanced{result: &swap.SendResult{Pending: true}, retry: true}
					}
				}
				// USDT and other reset-required tokens first approve zero, then
				// exactly this input amount. Unlimited approvals are never created.
				approval := input
				purpose = "approval"
				if allowance.Sign() > 0 {
					approval = new(big.Int)
					purpose = "approval_reset"
				}
				data, decodeErr = approvalData(funding.ApprovalAddress, approval)
				if decodeErr != nil {
					return fail(decodeErr)
				}
				to, value = token.Contract, new(big.Int)
			}
		}
		recordTo = to
		unsigned, err = builder.PrepareSwapCall(ctx, quote.Request.FromAddress, to, value, data)
	case "transfer":
		if funding.AmountRaw != quote.Request.AmountRaw || !adapter.ValidateAddress(funding.To) {
			return fail(swap.ErrInvalidQuote)
		}
		req := types.TransferRequest{From: quote.Request.FromAddress, To: funding.To, Amount: input, Asset: strings.ToLower(quote.Request.From.Symbol), Token: token, DestinationTag: funding.DestinationTag}
		if funding.Memo != "" {
			if memoErr := ValidateSwapMemo(funding.Memo, quote.Request); memoErr != nil {
				slog.Warn("swap funding memo refused by the custody guard", "order_id", order.ID, "provider", quote.Provider, "chain", wallet.Chain)
				return fail(memoErr)
			}
			memoBuilder, supported := adapter.(chain.MemoTransferBuilder)
			if !supported {
				return fail(swap.ErrUnsupported)
			}
			unsigned, err = memoBuilder.BuildMemoTransfer(ctx, req, funding.Memo)
		} else {
			unsigned, err = adapter.BuildTransfer(ctx, req)
		}
	case "solana":
		builder, supported := adapter.(chain.SolanaSwapBuilder)
		if !supported {
			return fail(swap.ErrUnsupported)
		}
		unsigned, err = builder.PrepareSolanaSwap(ctx, quote.Request.FromAddress, funding.Data, input, token)
		if errors.Is(err, chain.ErrSwapTransactionRefused) {
			slog.Warn("swap funding transaction refused by the custody guard", "order_id", order.ID, "provider", quote.Provider, "chain", wallet.Chain, "reason", err.Error())
			return fail(errors.Join(swap.ErrInvalidQuote, err))
		}
	default:
		return fail(swap.ErrUnsupported)
	}
	if err != nil {
		return fail(err)
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return fail(swap.ErrExpired)
	}
	for _, hold := range holds {
		if !hold.Held() {
			return fail(swap.ErrInProgress)
		}
	}
	signed, err := s.deps.Signer.SignPrepared(ctx, wallet.ID, in.Passphrase, adapter, unsigned)
	if err != nil {
		return fail(err)
	}
	raw, err := s.deps.Signer.PrepareBroadcast(ctx, adapter, signed)
	if err != nil {
		return fail(err)
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return fail(swap.ErrExpired)
	}
	for _, hold := range holds {
		if !hold.Held() {
			return fail(swap.ErrInProgress)
		}
	}
	txID := uuid.New()
	step := &models.SwapExecutionStep{ID: uuid.New(), OrderID: order.ID, TransactionID: txID, Position: len(steps), Purpose: purpose, TxHash: signed.TxHash}
	if signed.TxHash == "" {
		return fail(swap.ErrInvalidQuote)
	}
	quantity, asset, contract := quote.Request.AmountRaw, strings.ToLower(quote.Request.From.Symbol), quote.Request.From.Contract
	if purpose != "funding" {
		quantity, asset, contract = "0", adapter.NativeAsset(), ""
	}
	key := "swap:" + order.ID.String() + ":" + fmt.Sprint(len(steps))
	tx := &models.Transaction{ID: txID, WalletID: wallet.ID, AddressID: &wallet.DepositAddress.ID, Chain: wallet.Chain, TxType: models.TxTypeWithdrawal, TxHash: signed.TxHash, LogIndex: models.TransactionLogIndexNone, FromAddress: quote.Request.FromAddress, ToAddress: recordTo, Amount: quantity, Asset: asset, TokenContract: contract, Status: string(types.TxStatusConfirming), RequiredConfs: int(adapter.RequiredConfirmations()), Finality: chain.FinalityPolicyOf(adapter), Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, Origin: "swap", RawPayload: raw, IdempotencyKey: &key}
	if err := s.deps.Steps.RecordSigned(ctx, step, tx); err != nil {
		return fail(err)
	}
	// Once durable bytes exist, an error can never cause a new transaction or a
	// provider fallback. SettleFunding resends these exact bytes when unanswered.
	_, sendErr := adapter.BroadcastTransaction(ctx, signed)
	if sendErr == nil {
		s.recordAccepted(ctx, txID, signed.TxHash, raw)
	}
	if purpose != "funding" {
		if sendErr != nil {
			return pending(sendErr)
		}
		return advanced{result: &swap.SendResult{Pending: true}, await: signed.TxHash}
	}
	return advanced{result: &swap.SendResult{Hash: signed.TxHash, Uncertain: sendErr != nil}, err: sendErr}
}

// broadcastRecorder records a send's answer on its transaction row.
type broadcastRecorder interface {
	RecordBroadcast(context.Context, uuid.UUID, string, string) error
}

// recordAccepted keeps the accepted answer of a send and drops its signed bytes,
// as a withdrawal does: SettleFunding then never resends an answered send (a
// fallback node's refusal of those bytes could otherwise fail a sent funding).
// A write that fails leaves the pending outcome, which SettleFunding settles.
func (s *Service) recordAccepted(ctx context.Context, id uuid.UUID, hash, raw string) {
	recorder, ok := s.deps.Transactions.(broadcastRecorder)
	if !ok {
		return
	}
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &payload)
	delete(payload, chain.SignedTxPayloadKey)
	payload[chain.BroadcastOutcomePayloadKey] = chain.BroadcastOutcomeAccepted
	encoded, err := json.Marshal(payload)
	if err == nil {
		err = recorder.RecordBroadcast(ctx, id, hash, string(encoded))
	}
	if err != nil {
		slog.Warn("swap send answer not recorded; it stays pending for SettleFunding", "tx_id", id, "error", err.Error())
	}
}

// waitDeadline is when an in-request approval wait must give up: InclusionWait,
// a margin before the quote expires, and a margin before the request deadline.
func (s *Service) waitDeadline(ctx context.Context, quote *swap.Quote) time.Time {
	if s.deps.InclusionWait <= 0 || quote == nil {
		return time.Time{}
	}
	const margin = 5 * time.Second
	deadline := time.Now().Add(s.deps.InclusionWait)
	if expiry := quote.ExpiresAt.Add(-margin); expiry.Before(deadline) {
		deadline = expiry
	}
	if request, ok := ctx.Deadline(); ok && request.Add(-margin).Before(deadline) {
		deadline = request.Add(-margin)
	}
	return deadline
}

// awaitInclusion reports whether the approval was included (or failed on chain,
// which the next pass records) before the deadline.
func (s *Service) awaitInclusion(ctx context.Context, wallet *models.Wallet, hash string, holds []*cacheguard.Hold, deadline time.Time) bool {
	adapter, err := s.deps.Registry.ChainForWallet(wallet)
	if err != nil {
		return false
	}
	for s.pause(ctx, holds, deadline) {
		block, receiptErr := adapter.GetTransactionBlock(ctx, hash)
		if errors.Is(receiptErr, chain.ErrTransactionFailed) || (receiptErr == nil && block > 0) {
			return true
		}
	}
	return false
}

// pause waits one poll interval; false once the deadline, the request or a hold ends.
func (s *Service) pause(ctx context.Context, holds []*cacheguard.Hold, deadline time.Time) bool {
	if deadline.IsZero() || !time.Now().Add(s.deps.PollInterval).Before(deadline) {
		return false
	}
	timer := time.NewTimer(s.deps.PollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	for _, hold := range holds {
		if !hold.Held() {
			return false
		}
	}
	return true
}

// SettleFunding reports what the source chain shows of the order's funding
// transaction, after resending its exact signed bytes when the send was never
// answered (a crash between persisting and broadcasting). It never signs.
func (s *Service) SettleFunding(ctx context.Context, order *models.SwapOrder, wallet *models.Wallet) (swap.FundingState, error) {
	if s.deps.Steps == nil || s.deps.Transactions == nil || s.deps.Registry == nil {
		return swap.FundingUnknown, nil
	}
	steps, err := s.deps.Steps.ExecutionSteps(ctx, order.ID)
	if err != nil {
		return swap.FundingUnknown, err
	}
	for _, step := range steps {
		if step.Purpose != "funding" {
			continue
		}
		tx, err := s.deps.Transactions.FindByID(ctx, step.TransactionID)
		if err != nil {
			return swap.FundingUnknown, err
		}
		if state, settled := fundingStateOf(tx); settled {
			return state, nil
		}
		if s.deps.Settler != nil && sendUnanswered(tx) {
			settledTx, settleErr := s.deps.Settler.SettleUnanswered(ctx, tx)
			if settleErr != nil {
				slog.Warn("swap funding send still unverified", "order_id", order.ID, "tx_hash", tx.TxHash, "error", settleErr.Error())
			} else if state, settled := fundingStateOf(settledTx); settled {
				return state, nil
			}
		}
		adapter, err := s.deps.Registry.ChainForWallet(wallet)
		if err != nil {
			return swap.FundingUnknown, err
		}
		block, receiptErr := adapter.GetTransactionBlock(ctx, step.TxHash)
		switch {
		case errors.Is(receiptErr, chain.ErrTransactionFailed):
			return swap.FundingFailed, nil
		case receiptErr == nil && block > 0:
			return swap.FundingIncluded, nil
		}
		return swap.FundingUnknown, nil
	}
	return swap.FundingUnknown, nil
}

func fundingStateOf(tx *models.Transaction) (swap.FundingState, bool) {
	if tx == nil {
		return swap.FundingUnknown, false
	}
	switch tx.Status {
	case string(types.TxStatusConfirmed):
		return swap.FundingIncluded, true
	case string(types.TxStatusFailed):
		return swap.FundingFailed, true
	}
	return swap.FundingUnknown, false
}

// sendUnanswered is a row recorded before its send whose answer was never recorded.
func sendUnanswered(tx *models.Transaction) bool {
	payload := map[string]any{}
	if json.Unmarshal([]byte(tx.RawPayload), &payload) != nil {
		return false
	}
	_, kept := payload[chain.SignedTxPayloadKey]
	return kept && payload[chain.BroadcastOutcomePayloadKey] == chain.BroadcastOutcomePending
}

func preSend(err error) error { return errors.Join(swap.ErrExecutionFailed, err) }
func parseValue(value string) (*big.Int, bool) {
	if strings.HasPrefix(value, "0x") {
		parsed, ok := new(big.Int).SetString(strings.TrimPrefix(value, "0x"), 16)
		return parsed, ok && parsed.Sign() >= 0
	}
	parsed, ok := amount.ParseBaseUnits(value)
	return parsed, ok && parsed.Sign() >= 0
}
func approvalData(spender string, value *big.Int) ([]byte, error) {
	address, err := hex.DecodeString(strings.TrimPrefix(spender, "0x"))
	if err != nil || len(address) != 20 || value.BitLen() > 256 {
		return nil, swap.ErrInvalidQuote
	}
	data := make([]byte, 68)
	copy(data, []byte{0x09, 0x5e, 0xa7, 0xb3})
	copy(data[16:36], address)
	value.FillBytes(data[36:])
	return data, nil
}
