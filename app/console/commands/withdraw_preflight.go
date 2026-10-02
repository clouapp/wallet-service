package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/sweep"
)

const (
	preflightModeWithdrawal    = "withdrawal"
	preflightModeConsolidation = "consolidation"
	preflightMaxRequestBytes   = 64 << 10
)

// WithdrawPreflight signs, verifies, and prints what a withdrawal or consolidation
// would broadcast, without broadcasting or writing anything. The request, which
// carries the wallet passphrase, is read from stdin so it never appears in argv.
type WithdrawPreflight struct{}

type preflightRequest struct {
	Mode       string `json:"mode"`
	WalletID   string `json:"wallet_id"`
	Asset      string `json:"asset"`
	Amount     string `json:"amount"`
	To         string `json:"to"`
	Passphrase string `json:"passphrase"`
}

type preflightTxOutput struct {
	Role   string `json:"role"`
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount,omitempty"`
	TxHash string `json:"tx_hash"`
}

type preflightOutput struct {
	Mode              string              `json:"mode"`
	WalletID          string              `json:"wallet_id"`
	Strategy          string              `json:"strategy"`
	Asset             string              `json:"asset"`
	EstimatedGas      string              `json:"estimated_gas,omitempty"`
	SignatureVerified bool                `json:"signature_verified"`
	Broadcast         bool                `json:"broadcast"`
	Transactions      []preflightTxOutput `json:"transactions"`
}

func (c *WithdrawPreflight) Signature() string {
	return "withdraw:preflight"
}

func (c *WithdrawPreflight) Description() string {
	return "Plan, sign and verify a withdrawal or consolidation without broadcasting (JSON request on stdin)"
}

func (c *WithdrawPreflight) Extend() command.Extend {
	return command.Extend{Category: "withdraw"}
}

func (c *WithdrawPreflight) Handle(ctx console.Context) error {
	request, err := readPreflightRequest(os.Stdin)
	if err != nil {
		ctx.Error(err.Error())
		return err
	}
	output, err := runPreflight(context.Background(), request)
	if err != nil {
		ctx.Error("preflight failed: " + err.Error())
		return err
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode preflight: %w", err)
	}
	fmt.Println("PREFLIGHT " + string(encoded))
	return nil
}

func readPreflightRequest(input io.Reader) (preflightRequest, error) {
	raw, err := io.ReadAll(io.LimitReader(input, preflightMaxRequestBytes))
	if err != nil {
		return preflightRequest{}, fmt.Errorf("read preflight request: %w", err)
	}
	var request preflightRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return preflightRequest{}, fmt.Errorf("preflight request is not JSON")
	}
	request.Mode = strings.TrimSpace(request.Mode)
	request.To = strings.TrimSpace(request.To)
	request.Asset = strings.TrimSpace(request.Asset)
	if request.Mode != preflightModeWithdrawal && request.Mode != preflightModeConsolidation {
		return preflightRequest{}, fmt.Errorf("mode must be %q or %q", preflightModeWithdrawal, preflightModeConsolidation)
	}
	if _, err := uuid.Parse(request.WalletID); err != nil {
		return preflightRequest{}, fmt.Errorf("wallet_id is not a UUID")
	}
	if request.Asset == "" {
		return preflightRequest{}, fmt.Errorf("asset is required")
	}
	if len(request.Passphrase) < 12 {
		return preflightRequest{}, fmt.Errorf("passphrase must be at least 12 characters")
	}
	if request.Mode == preflightModeWithdrawal {
		amount, ok := new(big.Int).SetString(request.Amount, 10)
		if !ok || amount.Sign() <= 0 {
			return preflightRequest{}, fmt.Errorf("amount must be a positive integer in base units")
		}
		if request.To == "" {
			return preflightRequest{}, fmt.Errorf("to is required for a withdrawal")
		}
	}
	return request, nil
}

func runPreflight(ctx context.Context, request preflightRequest) (*preflightOutput, error) {
	ctr := container.Get()
	preflighter, ok := ctr.SweepService.(sweep.Preflighter)
	if !ok {
		return nil, fmt.Errorf("sweep service does not support preflight")
	}
	walletID := uuid.MustParse(request.WalletID)

	var result *sweep.Preflight
	var err error
	switch request.Mode {
	case preflightModeWithdrawal:
		result, err = preflightWithdrawal(ctx, ctr, preflighter, walletID, request)
	default:
		result, err = preflighter.PreflightConsolidation(ctx, walletID, request.Asset, request.Passphrase)
	}
	if err != nil {
		return nil, err
	}
	return newPreflightOutput(request, result), nil
}

func preflightWithdrawal(ctx context.Context, ctr *container.Container, preflighter sweep.Preflighter, walletID uuid.UUID, request preflightRequest) (*sweep.Preflight, error) {
	wallet, err := ctr.WalletRepo.FindByID(context.Background(), walletID)
	if err != nil || wallet == nil {
		return nil, fmt.Errorf("wallet %s not found", walletID)
	}
	adapter, err := ctr.Registry.Chain(wallet.Chain)
	if err != nil {
		return nil, err
	}
	if !adapter.ValidateAddress(request.To) {
		return nil, fmt.Errorf("invalid address for chain %s", wallet.Chain)
	}
	amount, _ := new(big.Int).SetString(request.Amount, 10)
	plan, err := ctr.SweepService.PlanForWithdrawal(ctx, walletID, request.Asset, amount, request.To, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("plan withdrawal: %w", err)
	}
	return preflighter.PreflightWithdrawal(ctx, plan, request.Passphrase, request.To)
}

func newPreflightOutput(request preflightRequest, result *sweep.Preflight) *preflightOutput {
	output := &preflightOutput{
		Mode:              request.Mode,
		WalletID:          request.WalletID,
		Strategy:          string(result.Strategy),
		Asset:             result.Asset,
		SignatureVerified: len(result.Transactions) > 0,
		Broadcast:         false,
		Transactions:      make([]preflightTxOutput, 0, len(result.Transactions)),
	}
	if result.EstimatedGas != nil {
		output.EstimatedGas = result.EstimatedGas.String()
	}
	for _, tx := range result.Transactions {
		entry := preflightTxOutput{Role: tx.Role, From: tx.From, To: tx.To, TxHash: tx.TxHash}
		if tx.Amount != nil {
			entry.Amount = tx.Amount.String()
		}
		output.Transactions = append(output.Transactions, entry)
	}
	return output
}
