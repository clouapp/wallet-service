package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/queue"
)

// walletPayload is the decoded wallet id and chain id a refresh job accepts.
type walletPayload struct {
	WalletID uuid.UUID
	ChainID  string
}

// walletDocument is the one JSON object stored on the queue.
type walletDocument struct {
	WalletID string `json:"wallet_id"`
	ChainID  string `json:"chain_id"`
}

// walletQueue is the one service each wallet job calls after decode.
type walletQueue interface {
	RefreshBalances(ctx context.Context, walletID uuid.UUID, chainID string) error
	RefreshTransactions(ctx context.Context, walletID uuid.UUID, chainID string) error
	RefreshTokens(ctx context.Context, walletID uuid.UUID, chainID string) error
	RefreshUTXOs(ctx context.Context, walletID uuid.UUID, chainID string) error
	ReconcileWallet(ctx context.Context, walletID uuid.UUID, chainID string) error
}

// WalletArgs is the only payload a wallet refresh or reconcile job accepts.
func WalletArgs(walletID, chainID string) ([]queue.Arg, error) {
	return encode(walletDocument{WalletID: walletID, ChainID: chainID})
}

func encode(payload any) ([]queue.Arg, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []queue.Arg{{Type: "string", Value: string(raw)}}, nil
}

func decodeWalletPayload(job string, args []any) (walletPayload, error) {
	var doc walletDocument
	if err := decode(args, &doc); err != nil {
		return walletPayload{}, fmt.Errorf("%s: %w", job, err)
	}
	if doc.WalletID == "" {
		return walletPayload{}, fmt.Errorf("%s: wallet_id must be a non-empty string", job)
	}
	if doc.ChainID == "" {
		return walletPayload{}, fmt.Errorf("%s: chain_id must be a non-empty string", job)
	}
	walletID, err := uuid.Parse(doc.WalletID)
	if err != nil {
		return walletPayload{}, fmt.Errorf("%s: invalid wallet_id: %w", job, err)
	}
	return walletPayload{WalletID: walletID, ChainID: doc.ChainID}, nil
}

// decode unmarshals the one JSON object a job was given. Field positions in
// args are not read.
func decode(args []any, dest any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return errors.New("payload is not one json object")
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(raw, &documents); err != nil || len(documents) != 1 {
		return errors.New("payload is not one json object")
	}
	body := documents[0]
	var text string
	if err := json.Unmarshal(body, &text); err == nil {
		body = []byte(text)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("payload is not one json object")
	}
	return nil
}
