package withdrawrecoveryruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	evmtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/app/models"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/pkg/types"
)

// SendExact never builds, signs, selects a nonce, retries or updates any record.
// The domain must already have durably claimed and sealed this exact candidate.
func (r *Runtime) SendExact(ctx context.Context, b recovery.Binding, raw []byte) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > 131072 {
		return "", refuse("bounded signed bytes required")
	}
	raw = bytes.Clone(raw)
	defer clear(raw)
	nonce := *b.Nonce
	b.Nonce = &nonce
	var tx evmtypes.Transaction
	if tx.UnmarshalBinary(raw) != nil || tx.Type() != evmtypes.LegacyTxType || tx.ChainId().Cmp(big.NewInt(b.NetworkID)) != 0 || tx.Nonce() != *b.Nonce || tx.To() == nil || *tx.To() != common.HexToAddress(b.TokenContract) || tx.Value().Sign() != 0 {
		return "", refuse("signed transaction differs from frozen binding")
	}
	signer, err := evmtypes.Sender(evmtypes.LatestSignerForChainID(tx.ChainId()), &tx)
	if err != nil || signer != common.HexToAddress(b.SourceAddress) {
		return "", refuse("signed sender differs")
	}
	units, err := positive(b.AmountRaw)
	if err != nil {
		return "", err
	}
	data := make([]byte, 68)
	copy(data, []byte{0xa9, 0x05, 0x9c, 0xbb})
	copy(data[16:36], common.HexToAddress(b.Recipient).Bytes())
	units.FillBytes(data[36:68])
	if !bytes.Equal(data, tx.Data()) || tx.Gas() == 0 || tx.GasPrice().Sign() <= 0 {
		return "", refuse("signed token transfer differs")
	}
	rec, err := r.read(ctx, b.OperationID, b.OriginalTransactionID)
	if err != nil {
		return "", err
	}
	if rec.candidateHash == "" || !strings.EqualFold(rec.candidateHash, tx.Hash().Hex()) {
		return "", refuse("exact candidate must be durably linked before transmission")
	}
	if (rec.withdrawal.Status != "broadcasting" && rec.withdrawal.Status != "broadcast") || rec.candidate == nil || rec.candidate.Status != "pending" {
		return "", recovery.ErrNeedsReconciliation
	}
	var kept struct {
		Attempt string `json:"recovery_attempt_id"`
		Outcome string `json:"broadcast_outcome"`
		Signed  string `json:"signed_tx"`
	}
	if json.Unmarshal([]byte(rec.candidate.RawPayload), &kept) != nil || (kept.Outcome != "pending" && kept.Outcome != "unknown") || kept.Signed == "" {
		return "", refuse("candidate sealed-send receipt is missing")
	}
	if r.deps.Attempts == nil {
		return "", refuse("durable transmission claim repository required")
	}
	claim, err := r.deps.Attempts.FindByOperation(ctx, b.OperationID)
	if err != nil {
		return "", recovery.ErrNeedsReconciliation
	}
	if claim == nil || claim.State != models.RecoverySendClaimed || claim.OperationID != b.OperationID || claim.OriginalTransactionID != b.OriginalTransactionID || claim.AccountID != b.AccountID || claim.WalletID != b.WalletID || claim.SourceAddressID != b.SourceAddressID || claim.NetworkID != b.NetworkID || !strings.EqualFold(claim.Sender, b.SourceAddress) || claim.SenderNonce != strconv.FormatUint(*b.Nonce, 10) || claim.BindingHash != digest(b) || claim.CandidateTransactionID == nil || *claim.CandidateTransactionID != rec.candidate.ID || !strings.EqualFold(claim.CandidateHash, tx.Hash().Hex()) || claim.SealedSignedTx == "" || claim.SealedSignedTx != kept.Signed || claim.ID.String() != kept.Attempt {
		return "", refuse("durable exact-byte transmission claim differs")
	}
	if digest(rec.binding) != digest(b) {
		return "", refuse("server-derived binding changed before send")
	}
	maxReviewedFee, reviewExpiresAt, err := r.reviewedEncodedFee(claim, b, &tx)
	if err != nil {
		return "", err
	}
	observation, err := r.observe(ctx, rec, tx.Hash().Hex())
	if err != nil {
		return "", err
	}
	if !observation.RPCsAgree || observation.OriginalKnown || observation.CandidateKnown || observation.ReceiptObserved || observation.LatestNonce == nil || observation.PendingNonce == nil || *observation.LatestNonce != *b.Nonce || *observation.PendingNonce != *b.Nonce {
		return "", recovery.ErrNeedsReconciliation
	}
	tokens, _ := new(big.Int).SetString(observation.SourceTokenBalanceRaw, 10)
	if tokens == nil || tokens.Cmp(units) < 0 {
		return "", refuse("source token balance changed")
	}
	// Check the encoded transaction's cost, not a different freshly rebuilt fee.
	cost := new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas()))
	if l1, ok := rec.adapter.(types.L1DataFeeEstimator); ok {
		limit := tx.Gas()
		req := types.TransferRequest{From: b.SourceAddress, To: b.Recipient, Amount: units, Asset: b.Asset, Token: rec.token, GasLimit: &limit, GasPrice: tx.GasPrice()}
		fee, err := l1.EstimateL1DataFee(ctx, req)
		if err != nil || fee == nil || fee.Sign() < 0 {
			return "", refuse("encoded L1 fee unavailable")
		}
		cost.Add(cost, fee)
	}
	if cost.Cmp(maxReviewedFee) > 0 {
		return "", refuse("encoded current fee exceeds immutable review")
	}
	native, _ := new(big.Int).SetString(observation.SourceNativeBalanceWei, 10)
	if native == nil || native.Cmp(cost) < 0 {
		return "", recovery.ErrGasNotReady
	}
	// Re-read both networks and nonces immediately before the single submission.
	for _, e := range r.deps.Endpoints {
		network, err := callHex(ctx, e.Client, "eth_chainId")
		if err != nil || network.Cmp(big.NewInt(b.NetworkID)) != 0 {
			return "", refuse("RPC network moved before send")
		}
		for _, tag := range []string{"latest", "pending"} {
			nonce, err := callHex(ctx, e.Client, "eth_getTransactionCount", b.SourceAddress, tag)
			if err != nil || !nonce.IsUint64() || nonce.Uint64() != *b.Nonce {
				return "", recovery.ErrNeedsReconciliation
			}
		}
	}
	if r.deps.Now().Sub(observation.ObservedAt) > recovery.MaxObservationAge {
		return "", refuse("send observation became stale")
	}
	if !reviewExpiresAt.After(r.deps.Now()) {
		return "", refuse("immutable fee review expired before transmission")
	}
	var returned string
	if r.deps.Endpoints[0].Client.Call(ctx, "eth_sendRawTransaction", &returned, "0x"+hex.EncodeToString(raw)) != nil {
		return "", recovery.ErrNeedsReconciliation
	}
	if !strings.EqualFold(returned, tx.Hash().Hex()) {
		return "", recovery.ErrNeedsReconciliation
	}
	return tx.Hash().Hex(), nil
}

var _ recovery.Observer = (*Runtime)(nil)
var _ recovery.Sender = (*Runtime)(nil)

// The current quote can use a different gas limit than the already signed call.
// Independently bind the encoded limits and its fresh L1 cost to the immutable
// proposal instead of treating a sufficient native balance as fee approval.
func (r *Runtime) reviewedEncodedFee(claim *models.WithdrawalRecoveryAttempt, b recovery.Binding, tx *evmtypes.Transaction) (*big.Int, time.Time, error) {
	var proposal recovery.Proposal
	if json.Unmarshal([]byte(claim.ProposalJSON), &proposal) != nil || digest(proposal) != claim.ProposalHash || digest(proposal.Binding) != digest(b) || proposal.CodeRevision != r.deps.CodeRevision || !proposal.ExpiresAt.After(r.deps.Now()) {
		return nil, time.Time{}, refuse("immutable fee proposal unavailable or changed")
	}
	reviewExpiry, reviewErr := ApprovedReviewExpiry(claim, proposal, r.deps.Now())
	if reviewErr != nil {
		return nil, time.Time{}, reviewErr
	}
	if validationErr := proposal.Fees.Validate(); validationErr != nil {
		return nil, time.Time{}, validationErr
	}
	var frozen struct {
		Nonce    *uint64 `json:"nonce"`
		To       string  `json:"to"`
		Value    string  `json:"value"`
		Data     string  `json:"data"`
		GasLimit uint64  `json:"gas_limit"`
		GasPrice string  `json:"gas_price"`
		L1Fee    string  `json:"l1_fee"`
	}
	if json.Unmarshal([]byte(claim.CallJSON), &frozen) != nil || frozen.Nonce == nil || *frozen.Nonce != tx.Nonce() || !strings.EqualFold(frozen.To, b.TokenContract) || frozen.Value != "0" || frozen.Data != hex.EncodeToString(tx.Data()) || frozen.GasLimit != tx.Gas() || frozen.GasPrice != tx.GasPrice().String() {
		return nil, time.Time{}, refuse("encoded transaction differs from immutable exact call")
	}
	maxPrice, err := positive(proposal.Fees.MaxGasPriceWei)
	if err != nil {
		return nil, time.Time{}, err
	}
	maxFee, err := positive(proposal.Fees.MaxNativeFeeWei)
	if err != nil {
		return nil, time.Time{}, err
	}
	l1, ok := new(big.Int).SetString(frozen.L1Fee, 10)
	if !ok || l1.Sign() < 0 || l1.BitLen() > 256 || tx.Gas() > proposal.Fees.MaxGasLimit || tx.GasPrice().Cmp(maxPrice) > 0 {
		return nil, time.Time{}, refuse("encoded limits exceed immutable review")
	}
	originalCost := new(big.Int).Add(new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas())), l1)
	if originalCost.Cmp(maxFee) > 0 {
		return nil, time.Time{}, refuse("frozen encoded fee exceeds immutable review")
	}
	return maxFee, reviewExpiry, nil
}
