// Package swapcustody funds pinned swap routes using the existing MPC custody.
package swapcustody

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
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

type Deps struct {
	Authority    Authority
	Registry     Registry
	Steps        Steps
	Transactions Transactions
	Signer       sweep.PreparedSigner
	Cache        contractscache.Driver
}

type Service struct{ deps Deps }

func New(deps Deps) *Service { return &Service{deps: deps} }

func (s *Service) Authorize(ctx context.Context, wallet *models.Wallet, in swap.ExecuteInput) error {
	return s.deps.Authority.AuthorizeSwap(ctx, wallet, in)
}

// Send advances at most one signed transaction. Approval inclusion is checked by
// the existing confirmation tracker before funding can be signed. Every retry
// first reads durable steps and never generates another funding transaction.
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
	steps, err := s.deps.Steps.ExecutionSteps(ctx, order.ID)
	if err != nil {
		return nil, preSend(err)
	}
	for _, step := range steps {
		if step.Purpose == "funding" {
			return &swap.SendResult{Hash: step.TxHash, Uncertain: true}, nil
		}
		tx, readErr := s.deps.Transactions.FindByID(ctx, step.TransactionID)
		if readErr != nil {
			return &swap.SendResult{Pending: true}, readErr
		}
		if tx.Status == string(types.TxStatusFailed) {
			return nil, preSend(fmt.Errorf("swap approval failed"))
		}
		if tx.Status != string(types.TxStatusConfirmed) {
			// An EVM approval can fund the next nonce once successfully included.
			// The following RPC estimate and allowance read must also succeed; the
			// destination credit still waits for the full confirmation policy.
			if s.deps.Registry == nil {
				return &swap.SendResult{Pending: true}, nil
			}
			approvalChain, chainErr := s.deps.Registry.ChainForWallet(wallet)
			if chainErr != nil {
				return &swap.SendResult{Pending: true}, chainErr
			}
			block, receiptErr := approvalChain.GetTransactionBlock(ctx, step.TxHash)
			if errors.Is(receiptErr, chain.ErrTransactionFailed) {
				return nil, preSend(receiptErr)
			}
			if receiptErr != nil || block == 0 {
				return &swap.SendResult{Pending: true}, nil
			}
		}
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return nil, preSend(swap.ErrExpired)
	}
	adapter, err := s.deps.Registry.ChainForWallet(wallet)
	if err != nil {
		return nil, preSend(err)
	}
	input, ok := amount.ParseBaseUnits(quote.Request.AmountRaw)
	if !ok || input.Sign() <= 0 || wallet.DepositAddress == nil {
		return nil, preSend(swap.ErrInvalidRequest)
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
		return nil, preSend(err)
	}
	if balance == nil || balance.Amount == nil || balance.Amount.Cmp(input) < 0 {
		return nil, preSend(chain.Insufficient(fmt.Errorf("swap requires funded base address")))
	}
	if len(steps) == 0 {
		if err := s.deps.Authority.ReserveSwapSpend(ctx, wallet, quote, in); err != nil {
			return nil, preSend(err)
		}
	}
	purpose := "funding"
	recordTo := funding.To
	var unsigned *types.UnsignedTx
	switch funding.Kind {
	case "evm":
		builder, supported := adapter.(chain.SwapCallBuilder)
		if !supported || funding.ChainID != quote.Request.From.NetworkID || funding.Memo != "" {
			return nil, preSend(swap.ErrUnsupported)
		}
		value, valid := parseValue(funding.AmountRaw)
		if !valid || (token == nil && value.Cmp(input) != 0) || (token != nil && value.Sign() != 0) {
			return nil, preSend(swap.ErrInvalidQuote)
		}
		data, decodeErr := hex.DecodeString(strings.TrimPrefix(funding.Data, "0x"))
		if decodeErr != nil {
			return nil, preSend(swap.ErrInvalidQuote)
		}
		to := funding.To
		if token != nil {
			if !adapter.ValidateAddress(funding.ApprovalAddress) {
				return nil, preSend(swap.ErrInvalidQuote)
			}
			allowance, allowanceErr := builder.SwapAllowance(ctx, token.Contract, quote.Request.FromAddress, funding.ApprovalAddress)
			if allowanceErr != nil {
				return nil, preSend(allowanceErr)
			}
			if allowance.Cmp(input) != 0 {
				for _, step := range steps {
					if step.Purpose == "approval" {
						return nil, preSend(fmt.Errorf("confirmed swap allowance changed before funding"))
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
					return nil, preSend(decodeErr)
				}
				to, value = token.Contract, new(big.Int)
			}
		}
		recordTo = to
		unsigned, err = builder.PrepareSwapCall(ctx, quote.Request.FromAddress, to, value, data)
	case "transfer":
		if funding.AmountRaw != quote.Request.AmountRaw || !adapter.ValidateAddress(funding.To) {
			return nil, preSend(swap.ErrInvalidQuote)
		}
		req := types.TransferRequest{From: quote.Request.FromAddress, To: funding.To, Amount: input, Asset: strings.ToLower(quote.Request.From.Symbol), Token: token, DestinationTag: funding.DestinationTag}
		if funding.Memo != "" {
			memoBuilder, supported := adapter.(chain.MemoTransferBuilder)
			if !supported {
				return nil, preSend(swap.ErrUnsupported)
			}
			unsigned, err = memoBuilder.BuildMemoTransfer(ctx, req, funding.Memo)
		} else {
			unsigned, err = adapter.BuildTransfer(ctx, req)
		}
	case "solana":
		builder, supported := adapter.(chain.SolanaSwapBuilder)
		if !supported {
			return nil, preSend(swap.ErrUnsupported)
		}
		unsigned, err = builder.PrepareSolanaSwap(ctx, quote.Request.FromAddress, funding.Data, input, token)
	default:
		return nil, preSend(swap.ErrUnsupported)
	}
	if err != nil {
		return nil, preSend(err)
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return nil, preSend(swap.ErrExpired)
	}
	for _, hold := range holds {
		if !hold.Held() {
			return nil, preSend(swap.ErrInProgress)
		}
	}
	signed, err := s.deps.Signer.SignPrepared(ctx, wallet.ID, in.Passphrase, adapter, unsigned)
	if err != nil {
		return nil, preSend(err)
	}
	raw, err := s.deps.Signer.PrepareBroadcast(ctx, adapter, signed)
	if err != nil {
		return nil, preSend(err)
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return nil, preSend(swap.ErrExpired)
	}
	for _, hold := range holds {
		if !hold.Held() {
			return nil, preSend(swap.ErrInProgress)
		}
	}
	txID := uuid.New()
	step := &models.SwapExecutionStep{ID: uuid.New(), OrderID: order.ID, TransactionID: txID, Position: len(steps), Purpose: purpose, TxHash: signed.TxHash}
	if signed.TxHash == "" {
		return nil, preSend(swap.ErrInvalidQuote)
	}
	quantity, asset, contract := quote.Request.AmountRaw, strings.ToLower(quote.Request.From.Symbol), quote.Request.From.Contract
	if purpose != "funding" {
		quantity, asset, contract = "0", adapter.NativeAsset(), ""
	}
	key := "swap:" + order.ID.String() + ":" + fmt.Sprint(len(steps))
	tx := &models.Transaction{ID: txID, WalletID: wallet.ID, AddressID: &wallet.DepositAddress.ID, Chain: wallet.Chain, TxType: models.TxTypeWithdrawal, TxHash: signed.TxHash, LogIndex: models.TransactionLogIndexNone, FromAddress: quote.Request.FromAddress, ToAddress: recordTo, Amount: quantity, Asset: asset, TokenContract: contract, Status: string(types.TxStatusConfirming), RequiredConfs: int(adapter.RequiredConfirmations()), Finality: chain.FinalityPolicyOf(adapter), Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, Origin: "swap", RawPayload: raw, IdempotencyKey: &key}
	if err := s.deps.Steps.RecordSigned(ctx, step, tx); err != nil {
		return nil, preSend(err)
	}
	// Once durable bytes exist, an error can never cause a new transaction or a
	// provider fallback. The tracker reconciles these exact bytes and their hash.
	_, sendErr := adapter.BroadcastTransaction(ctx, signed)
	if purpose != "funding" {
		return &swap.SendResult{Pending: true}, sendErr
	}
	return &swap.SendResult{Hash: signed.TxHash, Uncertain: sendErr != nil}, sendErr
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
