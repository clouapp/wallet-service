package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

const directChildGasPayloadKey = "direct_child_gas"

type directChildGasRecord struct {
	Binding      DirectChildGasBinding `json:"binding"`
	Plan         DirectChildGasPlan    `json:"plan"`
	BindingHash  string                `json:"binding_hash"`
	SignedFeeWei string                `json:"signed_fee_wei"`
}

type directGasSign func(context.Context, *directGasContext, *types.UnsignedTx) (*types.SignedTx, error)

func (s *service) gasPreparationStore() (directChildGasStore, error) {
	store, ok := s.txRepo.(directChildGasStore)
	if !ok || s.sealer == nil {
		return nil, fmt.Errorf("%w: durable gas preparation storage and sealing are required", ErrGasPreparationPending)
	}
	return store, nil
}

// PrepareDirectChildGas owns only this exact gas step. It never signs the token
// payment. An existing step is observed, not signed or transmitted again.
func (s *service) PrepareDirectChildGas(ctx context.Context, b DirectChildGasBinding, p DirectChildGasPlan, passphrase string) (*DirectChildGasResult, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	return s.prepareDirectChildGas(ctx, b, p, func(signCtx context.Context, g *directGasContext, unsigned *types.UnsignedTx) (*types.SignedTx, error) {
		session, err := s.openPreflightSession(signCtx, g.wallet.ID, b.Chain, passphrase)
		if err != nil {
			return nil, err
		}
		defer session.close()
		return s.signUnsigned(signCtx, g.adapter, session.curve, session.keys, session.wallet, *session.wallet.DepositAddress, unsigned)
	})
}

func (s *service) prepareDirectChildGas(ctx context.Context, b DirectChildGasBinding, p DirectChildGasPlan, sign directGasSign) (*DirectChildGasResult, error) {
	store, err := s.gasPreparationStore()
	if err != nil {
		return nil, err
	}
	if b.OperationID == uuid.Nil || b.ClaimID == uuid.Nil || sign == nil {
		return nil, ErrGasPreparationBinding
	}
	stepID := DirectChildGasStepID(b.OperationID)
	created := false
	err = store.WithinDirectChildGas(ctx, b.WalletID, stepID, func(txCtx context.Context) error {
		old, findErr := store.FindByID(txCtx, stepID)
		if findErr != nil && !errors.Is(findErr, models.ErrRepositoryNotFound) {
			return findErr
		}
		if old != nil {
			_, recordErr := readDirectGasRecord(old, b)
			return recordErr
		}
		if gateErr := s.gate(txCtx, b.AccountID); gateErr != nil {
			return gateErr
		}
		g, contextErr := s.directGasContext(txCtx, b)
		if contextErr != nil {
			return contextErr
		}
		if validateErr := validateDirectGasPlan(txCtx, g, b, p); validateErr != nil {
			return validateErr
		}
		amount, _ := gasPositive(p.AmountWei)
		unsigned, buildErr := g.adapter.BuildTransfer(txCtx, types.TransferRequest{From: p.BaseAddress, To: g.child.Address, Amount: amount, Asset: g.adapter.NativeAsset()})
		if buildErr != nil {
			return buildErr
		}
		nonce, fee, checkErr := gasUnsignedEnvelope(txCtx, g, b, p, unsigned)
		if checkErr != nil {
			return checkErr
		}
		occupied, nonceErr := store.DirectChildGasNonceOccupied(txCtx, stepID, b.Chain, p.BaseAddress, nonce)
		if nonceErr != nil {
			return nonceErr
		}
		if occupied {
			return fmt.Errorf("%w: funder nonce is already owned", ErrGasPreparationPending)
		}
		signed, signErr := sign(txCtx, g, unsigned)
		if signErr != nil {
			return signErr
		}
		if signed == nil || len(signed.RawBytes) == 0 || strings.TrimSpace(signed.TxHash) == "" {
			return ErrGasPreparationBinding
		}
		defer zeroBytes(signed.RawBytes)
		payload, payloadErr := s.pendingPayload(g.adapter, signed, s.broadcastHead(txCtx, g.adapter, b.Chain))
		if payloadErr != nil {
			return payloadErr
		}
		var raw map[string]json.RawMessage
		if decodeErr := json.Unmarshal([]byte(payload), &raw); decodeErr != nil {
			return decodeErr
		}
		if len(raw[chain.SignedTxPayloadKey]) == 0 {
			return fmt.Errorf("%w: signed bytes were not sealed", ErrGasPreparationPending)
		}
		record := directChildGasRecord{Binding: b, Plan: p, BindingHash: gasBindingHash(b), SignedFeeWei: fee.String()}
		raw[directChildGasPayloadKey], _ = json.Marshal(record)
		raw[chain.SenderNoncePayloadKey], _ = json.Marshal(nonce)
		payloadBytes, marshalErr := json.Marshal(raw)
		if marshalErr != nil {
			return marshalErr
		}
		key := stepID.String()
		baseID := g.wallet.DepositAddress.ID
		requiredConfs := g.adapter.RequiredConfirmations()
		if requiredConfs > uint64(math.MaxInt32) {
			return ErrGasPreparationBinding
		}
		row := &models.Transaction{ID: stepID, WalletID: b.WalletID, AddressID: &baseID, ExternalUserID: g.wallet.DepositAddress.ExternalUserID,
			Chain: b.Chain, TxType: models.TxTypeGasSeed, TxHash: signed.TxHash, LogIndex: models.TransactionLogIndexNone,
			FromAddress: p.BaseAddress, ToAddress: g.child.Address, Amount: p.AmountWei, Asset: g.adapter.NativeAsset(), Status: string(types.TxStatusConfirming),
			RequiredConfs: int(requiredConfs), Finality: chain.FinalityPolicyOf(g.adapter), IdempotencyKey: &key,
			Origin: models.TxOriginGasSeed, Direction: models.TxDirectionSelf, Source: models.TxSourceWithdrawalFlow, RawPayload: string(payloadBytes)}
		if b.OriginalTransactionID != uuid.Nil {
			parent := b.OriginalTransactionID
			row.ParentTransactionID = &parent
		}
		if _, verifyErr := verifyGasSeedBytes(row, record, signed.RawBytes); verifyErr != nil {
			return verifyErr
		}
		if createErr := s.txRepo.Create(txCtx, row); createErr != nil {
			return createErr
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The physical transaction above has committed. No network send occurs in it.
	if !created {
		return s.ObserveDirectChildGas(ctx, b)
	}
	return s.ResumeDirectChildGas(ctx, b)
}

func readDirectGasRecord(row *models.Transaction, b DirectChildGasBinding) (*directChildGasRecord, error) {
	if row == nil || b.OperationID == uuid.Nil || row.ID != DirectChildGasStepID(b.OperationID) || row.WalletID != b.WalletID || row.Chain != b.Chain || row.TxType != models.TxTypeGasSeed || row.Origin != models.TxOriginGasSeed || row.Direction != models.TxDirectionSelf || row.Source != models.TxSourceWithdrawalFlow || row.IdempotencyKey == nil || *row.IdempotencyKey != row.ID.String() {
		return nil, ErrGasPreparationBinding
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.RawPayload), &payload); err != nil {
		return nil, ErrGasPreparationBinding
	}
	var record directChildGasRecord
	if err := json.Unmarshal(payload[directChildGasPayloadKey], &record); err != nil {
		return nil, ErrGasPreparationBinding
	}
	if record.BindingHash != gasBindingHash(b) || record.BindingHash != gasBindingHash(record.Binding) || record.Plan.AmountWei != row.Amount || !strings.EqualFold(record.Plan.BaseAddress, row.FromAddress) || !strings.EqualFold(b.SourceAddress, row.ToAddress) {
		return nil, ErrGasPreparationBinding
	}
	if row.AddressID == nil || *row.AddressID != record.Plan.BaseAddressID || (b.OriginalTransactionID != uuid.Nil && (row.ParentTransactionID == nil || *row.ParentTransactionID != b.OriginalTransactionID)) {
		return nil, ErrGasPreparationBinding
	}
	return &record, nil
}

func verifyGasSeedBytes(row *models.Transaction, record directChildGasRecord, raw []byte) (uint64, error) {
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return 0, ErrGasPreparationBinding
	}
	b, p := record.Binding, record.Plan
	amount, err := gasPositive(p.AmountWei)
	if err != nil {
		return 0, err
	}
	maxFee, err := gasPositive(p.MaxFeeWei)
	if err != nil {
		return 0, err
	}
	signedFee, err := gasPositive(record.SignedFeeWei)
	if err != nil {
		return 0, err
	}
	if tx.Type() != gethtypes.LegacyTxType || tx.ChainId().Cmp(big.NewInt(b.NetworkID)) != 0 || !strings.EqualFold(tx.Hash().Hex(), row.TxHash) || tx.To() == nil || !strings.EqualFold(tx.To().Hex(), b.SourceAddress) || tx.Value().Cmp(amount) != 0 || len(tx.Data()) != 0 || tx.Gas() == 0 || tx.GasPrice().Sign() <= 0 || signedFee.Cmp(maxFee) > 0 || new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas())).Cmp(maxFee) > 0 {
		return 0, ErrGasPreparationBinding
	}
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(tx.ChainId()), &tx)
	if err != nil || !strings.EqualFold(sender.Hex(), p.BaseAddress) || (p.FunderNonce != nil && tx.Nonce() != *p.FunderNonce) {
		return 0, ErrGasPreparationBinding
	}
	var payload map[string]json.RawMessage
	_ = json.Unmarshal([]byte(row.RawPayload), &payload)
	var nonce *uint64
	if err = json.Unmarshal(payload[chain.SenderNoncePayloadKey], &nonce); err != nil || nonce == nil || *nonce != tx.Nonce() {
		return 0, ErrGasPreparationBinding
	}
	return *nonce, nil
}

func (s *service) loadDirectGas(ctx context.Context, b DirectChildGasBinding) (*models.Transaction, *directChildGasRecord, *directGasContext, error) {
	if b.OperationID == uuid.Nil || b.ClaimID == uuid.Nil {
		return nil, nil, nil, ErrGasPreparationBinding
	}
	store, err := s.gasPreparationStore()
	if err != nil {
		return nil, nil, nil, err
	}
	row, err := store.FindByID(ctx, DirectChildGasStepID(b.OperationID))
	if err != nil {
		return nil, nil, nil, err
	}
	record, err := readDirectGasRecord(row, b)
	if err != nil {
		return nil, nil, nil, err
	}
	g, err := s.directGasContext(ctx, b)
	if err != nil {
		return nil, nil, nil, err
	}
	if record.Plan.BaseAddressID != g.wallet.DepositAddress.ID || !strings.EqualFold(record.Plan.BaseAddress, g.wallet.DepositAddress.Address) || !types.SameAssetSymbol(row.Asset, g.adapter.NativeAsset()) {
		return nil, nil, nil, ErrGasPreparationBinding
	}
	return row, record, g, nil
}

// ObserveDirectChildGas performs no send or database/cache mutation. Confirmed
// means successful receipt inclusion plus currently payable child gas, not a
// certificate that the block satisfies a separate finality policy.
func (s *service) ObserveDirectChildGas(ctx context.Context, b DirectChildGasBinding) (*DirectChildGasResult, error) {
	row, record, g, err := s.loadDirectGas(ctx, b)
	if err != nil {
		return nil, err
	}
	raw, ok := s.keptSignedBytes(row)
	if !ok {
		return nil, ErrGasPreparationBinding
	}
	defer zeroBytes(raw)
	nonce, err := verifyGasSeedBytes(row, *record, raw)
	if err != nil {
		return nil, err
	}
	result := &DirectChildGasResult{TransactionID: row.ID, TxHash: row.TxHash, NetworkID: b.NetworkID, From: row.FromAddress, To: row.ToAddress, Nonce: &nonce, AmountWei: row.Amount, FeeWei: record.SignedFeeWei}
	if row.Status == string(types.TxStatusFailed) {
		return result, ErrGasPreparationPending
	}
	block, err := searchBlock(ctx, g.adapter, row.TxHash)
	if err != nil {
		return result, fmt.Errorf("%w: seed receipt: %w", ErrGasPreparationPending, err)
	}
	if block == 0 {
		return result, nil
	}
	result.Included = true
	ready, err := s.tokenSourceGasReady(ctx, g.adapter, &Plan{Chain: b.Chain, Asset: b.Asset, Amount: g.amount}, g.child.Address, b.Recipient)
	if err != nil {
		return result, err
	}
	result.Confirmed = ready
	return result, nil
}

// ResumeDirectChildGas can transmit only the bytes already committed for this
// operation. It never signs, changes nonce, reseeds or chooses another wallet.
func (s *service) ResumeDirectChildGas(ctx context.Context, b DirectChildGasBinding) (*DirectChildGasResult, error) {
	observed, err := s.ObserveDirectChildGas(ctx, b)
	if err != nil {
		return observed, err
	}
	if observed.Confirmed || observed.Included {
		return observed, nil
	}
	row, record, g, err := s.loadDirectGas(ctx, b)
	if err != nil {
		return nil, err
	}
	if row.Status == string(types.TxStatusFailed) {
		return observed, ErrGasPreparationPending
	}
	raw, ok := s.keptSignedBytes(row)
	if !ok {
		return nil, ErrGasPreparationBinding
	}
	defer zeroBytes(raw)
	if _, err = verifyGasSeedBytes(row, *record, raw); err != nil {
		return nil, err
	}
	if gateErr := s.gate(ctx, b.AccountID); gateErr != nil {
		return observed, gateErr
	}
	if readyErr := gasCommittedSeedReady(ctx, g, *record, raw); readyErr != nil {
		return observed, readyErr
	}
	_, sendErr := g.adapter.BroadcastTransaction(ctx, &types.SignedTx{ChainID: b.Chain, RawBytes: raw, TxHash: row.TxHash})
	if sendErr != nil {
		return observed, fmt.Errorf("%w: seed send outcome requires observation", ErrGasPreparationPending)
	}
	if waitErr := awaitInclusion(ctx, g.adapter, row.TxHash); waitErr != nil {
		return observed, fmt.Errorf("%w: seed inclusion requires observation", ErrGasPreparationPending)
	}
	return s.ObserveDirectChildGas(ctx, b)
}

func gasCommittedSeedReady(ctx context.Context, g *directGasContext, record directChildGasRecord, raw []byte) error {
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return ErrGasPreparationBinding
	}
	tokens, err := g.adapter.GetTokenBalance(ctx, g.child.Address, *g.token)
	if err != nil {
		return err
	}
	if tokens == nil || tokens.Amount == nil || tokens.Amount.Cmp(g.amount) < 0 {
		return ErrInsufficientFunds
	}
	fee := new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas()))
	if l1, ok := g.adapter.(types.L1DataFeeEstimator); ok {
		limit := tx.Gas()
		dataFee, feeErr := l1.EstimateL1DataFee(ctx, types.TransferRequest{From: record.Plan.BaseAddress, To: record.Binding.SourceAddress, Amount: tx.Value(), Asset: g.adapter.NativeAsset(), GasPrice: tx.GasPrice(), GasLimit: &limit})
		if feeErr != nil {
			return feeErr
		}
		if dataFee == nil || dataFee.Sign() < 0 {
			return ErrFeeQuoteUnavailable
		}
		fee.Add(fee, dataFee)
	}
	maxFee, err := gasPositive(record.Plan.MaxFeeWei)
	if err != nil || fee.Cmp(maxFee) > 0 {
		return ErrGasPreparationBinding
	}
	reserve, err := gasNonnegative(record.Plan.MinFunderBalanceWei)
	if err != nil {
		return err
	}
	if current := g.funderReserve(); current.Cmp(reserve) > 0 {
		reserve.Set(current)
	}
	required := new(big.Int).Add(tx.Value(), maxFee)
	required.Add(required, reserve)
	balance, err := nativeGasBalance(ctx, g.adapter, record.Plan.BaseAddress)
	if err != nil {
		return err
	}
	if balance.Cmp(required) < 0 {
		return ErrWalletNotGasReady
	}
	return nil
}

func gasUnsignedEnvelope(ctx context.Context, g *directGasContext, b DirectChildGasBinding, p DirectChildGasPlan, unsigned *types.UnsignedTx) (uint64, *big.Int, error) {
	if unsigned == nil || unsigned.ChainID != b.Chain {
		return 0, nil, ErrGasPreparationBinding
	}
	nonce, ok := unsigned.Metadata["nonce"].(uint64)
	if !ok || (p.FunderNonce != nil && nonce != *p.FunderNonce) {
		return 0, nil, ErrGasPreparationBinding
	}
	network, ok := unsigned.Metadata["chain_id"].(int64)
	if !ok || network != b.NetworkID {
		return 0, nil, ErrGasPreparationBinding
	}
	to, ok := unsigned.Metadata["to"].(string)
	if !ok || !strings.EqualFold(to, b.SourceAddress) {
		return 0, nil, ErrGasPreparationBinding
	}
	value, ok := unsigned.Metadata["value"].(string)
	if !ok || value != p.AmountWei {
		return 0, nil, ErrGasPreparationBinding
	}
	if data, hasData := unsigned.Metadata["data"].([]byte); hasData && len(data) > 0 {
		return 0, nil, ErrGasPreparationBinding
	}
	limit, ok := unsigned.Metadata["gas_limit"].(uint64)
	if !ok || limit == 0 {
		return 0, nil, ErrGasPreparationBinding
	}
	priceText, ok := unsigned.Metadata["gas_price"].(string)
	if !ok {
		return 0, nil, ErrGasPreparationBinding
	}
	price, err := gasPositive(priceText)
	if err != nil {
		return 0, nil, err
	}
	fee := new(big.Int).Mul(price, new(big.Int).SetUint64(limit))
	if estimator, ok := g.adapter.(types.L1DataFeeEstimator); ok {
		amount, _ := gasPositive(p.AmountWei)
		l1, feeErr := estimator.EstimateL1DataFee(ctx, types.TransferRequest{From: p.BaseAddress, To: b.SourceAddress, Amount: amount, Asset: g.adapter.NativeAsset(), GasPrice: price, GasLimit: &limit})
		if feeErr != nil {
			return 0, nil, feeErr
		}
		if l1 == nil || l1.Sign() < 0 {
			return 0, nil, ErrFeeQuoteUnavailable
		}
		fee.Add(fee, l1)
	}
	maxFee, err := gasPositive(p.MaxFeeWei)
	if err != nil || fee.Cmp(maxFee) > 0 {
		return 0, nil, ErrGasPreparationBinding
	}
	return nonce, fee, nil
}
