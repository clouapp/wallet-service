package chain

import (
	"context"
	"fmt"
	"math/big"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/pkg/types"
)

// splTokenAccountBytes is the size of an SPL token account; creating one costs
// its rent-exempt minimum.
const splTokenAccountBytes = 165

// solanaFeeProbeRecipient prices transfers whose recipient is not known yet. Its
// associated token accounts do not exist, so a token quote includes their creation.
const solanaFeeProbeRecipient = "11111111111111111111111111111111"

// SolanaFeeQuote prices one transfer as buildSolanaTransfer encodes it: one
// signature, plus the recipient's associated token account when it must be created.
type SolanaFeeQuote struct {
	Signatures              int
	LamportsPerSignature    int64
	AccountCreationLamports *big.Int
}

// Fee is the lamports the transfer costs its fee payer besides the amount.
func (q SolanaFeeQuote) Fee() *big.Int {
	fee := new(big.Int).Mul(big.NewInt(q.LamportsPerSignature), big.NewInt(int64(q.Signatures)))
	if q.AccountCreationLamports != nil {
		fee.Add(fee, q.AccountCreationLamports)
	}
	return fee
}

// QuoteTransferFee prices req with the same decisions buildSolanaTransfer makes: a
// native transfer pays one signature; a token transfer also funds the recipient's
// associated token account when destATAMissing says it must be created. An empty
// recipient uses a probe whose token accounts do not exist.
func (a *SolanaLive) QuoteTransferFee(ctx context.Context, req types.TransferRequest) (SolanaFeeQuote, error) {
	quote := SolanaFeeQuote{Signatures: 1, LamportsPerSignature: solanaNativeFeeLamports, AccountCreationLamports: new(big.Int)}
	if req.Token == nil {
		return quote, nil
	}
	recipient := req.To
	if recipient == "" {
		recipient = solanaFeeProbeRecipient
	}
	owner, err := solana.PublicKeyFromBase58(recipient)
	if err != nil {
		return SolanaFeeQuote{}, fmt.Errorf("sol fee quote: recipient: %w", err)
	}
	mint, err := solana.PublicKeyFromBase58(req.Token.Contract)
	if err != nil {
		return SolanaFeeQuote{}, fmt.Errorf("sol fee quote: mint: %w", err)
	}
	destATA, _, err := solana.FindAssociatedTokenAddress(owner, mint)
	if err != nil {
		return SolanaFeeQuote{}, fmt.Errorf("sol fee quote: recipient token account: %w", err)
	}
	create, _, err := a.destATAMissing(ctx, destATA.String())
	if err != nil {
		return SolanaFeeQuote{}, fmt.Errorf("sol fee quote: recipient token account: %w", err)
	}
	if !create {
		return quote, nil
	}
	rent, err := a.rentExemptMinimum(ctx, splTokenAccountBytes)
	if err != nil {
		return SolanaFeeQuote{}, err
	}
	quote.AccountCreationLamports = rent
	return quote, nil
}

func (a *SolanaLive) rentExemptMinimum(ctx context.Context, accountBytes int) (*big.Int, error) {
	var lamports uint64
	if err := a.rpc.Call(ctx, "getMinimumBalanceForRentExemption", &lamports, accountBytes,
		map[string]string{"commitment": solanaCommitmentFinalized}); err != nil {
		return nil, fmt.Errorf("sol rent-exempt minimum for %d bytes: %w", accountBytes, err)
	}
	return new(big.Int).SetUint64(lamports), nil
}
