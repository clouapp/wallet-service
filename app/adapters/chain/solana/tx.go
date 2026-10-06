package solana

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"

	"math/big"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const solanaNativeFeeLamports int64 = 5000

func buildSolanaNativeTx(from, to solana.PublicKey, lamports uint64, blockhash string) ([]byte, error) {
	hash, err := solana.HashFromBase58(blockhash)
	if err != nil {
		return nil, fmt.Errorf("sol blockhash: %w", err)
	}
	ix := system.NewTransferInstruction(lamports, from, to).Build()
	tx, err := solana.NewTransaction([]solana.Instruction{ix}, hash, solana.TransactionPayer(from))
	if err != nil {
		return nil, err
	}
	return tx.Message.MarshalBinary()
}

func buildSolanaSPLTx(owner, destOwner, mint solana.PublicKey, amount uint64, blockhash string, createDest bool) ([]byte, error) {
	hash, err := solana.HashFromBase58(blockhash)
	if err != nil {
		return nil, fmt.Errorf("sol blockhash: %w", err)
	}
	sourceATA, _, err := solana.FindAssociatedTokenAddress(owner, mint)
	if err != nil {
		return nil, fmt.Errorf("sol source ata: %w", err)
	}
	destATA, _, err := solana.FindAssociatedTokenAddress(destOwner, mint)
	if err != nil {
		return nil, fmt.Errorf("sol dest ata: %w", err)
	}
	ixs := make([]solana.Instruction, 0, 2)
	if createDest {
		ixs = append(ixs, associatedtokenaccount.NewCreateInstruction(owner, destOwner, mint).Build())
	}
	ixs = append(ixs, token.NewTransferInstruction(amount, sourceATA, destATA, owner, nil).Build())
	tx, err := solana.NewTransaction(ixs, hash, solana.TransactionPayer(owner))
	if err != nil {
		return nil, err
	}
	return tx.Message.MarshalBinary()
}

// SolanaSigningView returns the message bytes and the fee-payer public key.
// It does not sign. The custody service signs the message and calls AssembleSolana.
func (a *SolanaLive) SolanaSigningView(unsigned *types.UnsignedTx) (message, feePayer []byte, err error) {
	msg, err := solanaMessage(unsigned)
	if err != nil {
		return nil, nil, err
	}
	if msg.Header.NumRequiredSignatures != 1 {
		return nil, nil, fmt.Errorf("sol sign: expected exactly one signer, message requires %d", msg.Header.NumRequiredSignatures)
	}
	if len(msg.AccountKeys) == 0 {
		return nil, nil, fmt.Errorf("sol sign: fee payer is not the signing key")
	}
	content, err := msg.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("sol message: %w", err)
	}
	return content, append([]byte(nil), msg.AccountKeys[0][:]...), nil
}

// AssembleSolana attaches a 64-byte signature the custody service already produced.
// The message must require exactly one signer. The signature is checked against the
// fee payer already carried in the message.
func (a *SolanaLive) AssembleSolana(unsigned *types.UnsignedTx, signature []byte) (*types.SignedTx, error) {
	msg, err := solanaMessage(unsigned)
	if err != nil {
		return nil, err
	}
	if msg.Header.NumRequiredSignatures != 1 {
		return nil, fmt.Errorf("sol sign: expected exactly one signer, message requires %d", msg.Header.NumRequiredSignatures)
	}
	if len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("sol signature must be %d bytes", ed25519.SignatureSize)
	}
	tx := &solana.Transaction{Message: *msg, Signatures: []solana.Signature{solana.SignatureFromBytes(signature)}}
	if err := tx.VerifySignatures(); err != nil {
		return nil, fmt.Errorf("sol sign: %w", err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &types.SignedTx{ChainID: unsigned.ChainID, RawBytes: raw, TxHash: tx.Signatures[0].String()}, nil
}

func solanaMessage(unsigned *types.UnsignedTx) (*solana.Message, error) {
	if unsigned == nil || len(unsigned.RawBytes) == 0 {
		return nil, fmt.Errorf("sol sign: missing transaction")
	}
	msg := &solana.Message{}
	if err := msg.UnmarshalWithDecoder(bin.NewBinDecoder(unsigned.RawBytes)); err != nil {
		return nil, fmt.Errorf("sol message: %w", err)
	}
	return msg, nil
}

func (a *SolanaLive) buildSolanaTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	if req.Amount == nil {
		return nil, fmt.Errorf("amount is required")
	}
	if !req.Amount.IsUint64() {
		return nil, fmt.Errorf("amount overflows uint64")
	}
	from, err := solana.PublicKeyFromBase58(req.From)
	if err != nil {
		return nil, fmt.Errorf("sol from: %w", err)
	}
	to, err := solana.PublicKeyFromBase58(req.To)
	if err != nil {
		return nil, fmt.Errorf("sol to: %w", err)
	}
	var result struct {
		Value struct {
			Blockhash string `json:"blockhash"`
		} `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getLatestBlockhash", &result, map[string]string{"commitment": "finalized"}); err != nil {
		return nil, err
	}
	var raw []byte
	meta := map[string]interface{}{}
	if req.Token == nil {
		raw, err = buildSolanaNativeTx(from, to, req.Amount.Uint64(), result.Value.Blockhash)
	} else {
		mint, mintErr := solana.PublicKeyFromBase58(req.Token.Contract)
		if mintErr != nil {
			return nil, fmt.Errorf("sol mint: %w", mintErr)
		}
		destATA, _, ataErr := solana.FindAssociatedTokenAddress(to, mint)
		if ataErr != nil {
			return nil, fmt.Errorf("sol dest ata: %w", ataErr)
		}
		createDest, exists, acctErr := a.destATAMissing(ctx, destATA.String())
		if acctErr != nil {
			return nil, acctErr
		}
		meta["dest_ata_exists"] = exists
		raw, err = buildSolanaSPLTx(from, to, mint, req.Amount.Uint64(), result.Value.Blockhash, createDest)
	}
	if err != nil {
		return nil, err
	}
	return &types.UnsignedTx{ChainID: a.cfg.ChainIDStr, RawBytes: raw, Metadata: meta, TransferAmount: new(big.Int).Set(req.Amount)}, nil
}

func (a *SolanaLive) destATAMissing(ctx context.Context, ata string) (create bool, exists bool, err error) {
	var info struct {
		Value *struct{} `json:"value"`
	}
	callErr := a.rpc.Call(ctx, "getAccountInfo", &info, ata, map[string]string{"commitment": "finalized"})
	if callErr != nil {
		if errors.Is(callErr, chain.ErrNotFound) {
			return true, false, nil
		}
		return false, false, callErr
	}
	if info.Value == nil {
		return true, false, nil
	}
	return false, true, nil
}

func (a *SolanaLive) buildSolanaSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	fee := big.NewInt(solanaNativeFeeLamports)
	if req.Token != nil {
		if req.NativeBalance == nil || req.NativeBalance.Cmp(fee) < 0 {
			return nil, chain.Insufficient(fmt.Errorf("insufficient native for fee"))
		}
		if req.Amount == nil {
			return nil, fmt.Errorf("amount is required")
		}
		unsigned, err := a.buildSolanaTransfer(ctx, types.TransferRequest{
			From:   req.From,
			To:     req.To,
			Amount: req.Amount,
			Token:  req.Token,
		})
		if err != nil {
			return nil, err
		}
		return []types.UnsignedTx{*unsigned}, nil
	}
	var amount *big.Int
	if req.Amount != nil {
		amount = new(big.Int).Set(req.Amount)
	} else {
		if req.NativeBalance == nil {
			return nil, fmt.Errorf("missing native balance")
		}
		amount = new(big.Int).Sub(req.NativeBalance, fee)
	}
	if amount.Sign() <= 0 {
		return nil, chain.Insufficient(fmt.Errorf("insufficient native for fee"))
	}
	unsigned, err := a.buildSolanaTransfer(ctx, types.TransferRequest{
		From:   req.From,
		To:     req.To,
		Amount: amount,
	})
	if err != nil {
		return nil, err
	}
	return []types.UnsignedTx{*unsigned}, nil
}

func broadcastSolanaTx(ctx context.Context, a *SolanaLive, signed *types.SignedTx) (string, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return "", fmt.Errorf("sol broadcast: empty signed transaction")
	}
	encoded := base64.StdEncoding.EncodeToString(signed.RawBytes)
	var signature string
	if err := a.rpc.Call(ctx, "sendTransaction", &signature, encoded, map[string]string{"encoding": "base64"}); err != nil {
		return "", err
	}
	return signature, nil
}
