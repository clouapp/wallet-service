// Command swap-custody-testnet runs the custody side of a swap on a testnet for
// real: LI.FI and SwapKit refuse testnets, so a controlled funding instruction
// stands in for the provider's. It drives swapcustody.Service exactly as an
// accepted order does (custody guard, builders, MPC signing, the durable step
// journal, broadcast, SettleFunding) against the wallet's registered testnet
// nodes, and refuses any wallet that is not on a testnet in a test account.
//
// Each mode first submits a hostile instruction (the call the 2026-11-04 audit
// showed was signed), which must be refused before anything is signed, then the
// legitimate one, which is signed, broadcast and followed until included:
//
//	evm  --wallet ID --amount BASE_UNITS          WETH deposit() call on Sepolia (or WPOL on Amoy)
//	memo --wallet ID --amount SATS --to ADDRESS    BTC testnet transfer with a THORChain swap memo (OP_RETURN)
//	sol  --wallet ID --amount LAMPORTS --to ADDR   Solana devnet message (system transfer to a deposit address)
//
// The wallet passphrase is read from SWAP_E2E_PASSPHRASE, never a flag. Run it
// with the service environment of the database that holds the wallet.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/app/services/swapcustody"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/bootstrap"
)

// Wrapped native contracts the legitimate EVM call deposits into (deposit()).
var wrappedNative = map[string]string{
	"teth":     "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14", // WETH9 on Sepolia
	"tpolygon": "0x360ad4f9a9A8EFe9A8DCB5f461c4Cc1047E1Dcf9", // WPOL on Amoy
}

type report struct {
	Mode          string `json:"mode"`
	Chain         string `json:"chain"`
	Wallet        string `json:"wallet"`
	HostileOrder  string `json:"hostile_order"`
	HostileResult string `json:"hostile_result"`
	HostileSigned int    `json:"hostile_signed_steps"`
	Order         string `json:"order"`
	TxHash        string `json:"tx_hash"`
	Funding       string `json:"funding_state"`
	Elapsed       string `json:"elapsed"`
}

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: swap-custody-testnet <evm|memo|sol> --wallet ID --amount N [--to ADDRESS] [--dest ADDRESS]"))
	}
	mode := os.Args[1]
	flags := flag.NewFlagSet(mode, flag.ExitOnError)
	walletID := flags.String("wallet", "", "testnet wallet id")
	quantity := flags.String("amount", "", "input in base units")
	to := flags.String("to", "", "deposit address (memo, sol)")
	dest := flags.String("dest", "", "swap destination named in the memo (memo)")
	wait := flags.Duration("wait", 10*time.Minute, "how long to follow the funding transaction")
	_ = flags.Parse(os.Args[2:])
	passphrase := os.Getenv("SWAP_E2E_PASSPHRASE")
	if passphrase == "" || *walletID == "" || *quantity == "" {
		fail(fmt.Errorf("--wallet, --amount and SWAP_E2E_PASSPHRASE are required"))
	}
	bootstrap.Boot()
	ctx := context.Background()
	out, err := run(ctx, mode, *walletID, *quantity, *to, *dest, passphrase, *wait)
	encoded, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(encoded))
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "swap-custody-testnet:", err)
	os.Exit(1)
}

func run(ctx context.Context, mode, walletID, quantity, to, dest, passphrase string, wait time.Duration) (*report, error) {
	started := time.Now()
	id, err := uuid.Parse(walletID)
	if err != nil {
		return nil, err
	}
	wallet, err := container.MustMake[*repositories.WalletRepository]().FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	network, err := container.MustMake[*repositories.ChainRepository]().FindByID(ctx, wallet.Chain)
	if err != nil {
		return nil, err
	}
	if wallet.AccountID == nil {
		return nil, fmt.Errorf("wallet has no account")
	}
	account, err := container.MustMake[*repositories.AccountRepository]().FindByID(ctx, *wallet.AccountID)
	if err != nil {
		return nil, err
	}
	if !network.IsTestnet || account.Environment != models.EnvironmentTest {
		return nil, fmt.Errorf("refused: %s is not a testnet wallet of a test account", wallet.Chain)
	}
	if wallet.DepositAddress == nil {
		return nil, fmt.Errorf("wallet has no base address")
	}
	registry := container.MustMake[*chain.Registry]()
	rows := container.MustMake[*repositories.SwapRepository]()
	box := container.MustMake[*sweep.Box]()
	signer, ok := box.Service.(sweep.PreparedSigner)
	if !ok {
		return nil, fmt.Errorf("prepared signer unavailable")
	}
	custody := swapcustody.New(swapcustody.Deps{
		Authority: container.MustMake[*withdraw.Service](), Registry: registry, Steps: rows,
		Transactions: container.MustMake[*repositories.TransactionRepository](), Signer: signer,
		Cache: facades.Cache(), Tokens: registry, Settler: box.Service, InclusionWait: 20 * time.Second,
	})
	from := swap.Asset{Symbol: strings.ToUpper(network.NativeSymbol), Chain: network.ID, Decimals: network.NativeDecimals, Native: true, Testnet: true}
	if network.NetworkID != nil {
		from.NetworkID = *network.NetworkID
	}
	request := swap.QuoteRequest{From: from, To: swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true}, FromAddress: wallet.DepositAddress.Address, ToAddress: dest, AmountRaw: quantity, SlippageBPS: 100}
	if request.ToAddress == "" {
		request.ToAddress = wallet.DepositAddress.Address
	}
	hostile, legit, err := instructions(ctx, mode, network, wallet, registry, request, to)
	if err != nil {
		return nil, err
	}
	out := &report{Mode: mode, Chain: network.ID, Wallet: wallet.ID.String()}
	in := swap.ExecuteInput{Passphrase: passphrase}

	hostileOrder, hostileQuote, err := newOrder(ctx, rows, account, wallet, request)
	if err != nil {
		return out, err
	}
	out.HostileOrder = hostileOrder.ID.String()
	_, hostileErr := custody.Send(ctx, hostileOrder, wallet, hostileQuote, hostile, in)
	out.HostileResult = fmt.Sprint(hostileErr)
	signedSteps, _ := rows.ExecutionSteps(ctx, hostileOrder.ID)
	out.HostileSigned = len(signedSteps)
	if hostileErr == nil || !errors.Is(hostileErr, swap.ErrInvalidQuote) || len(signedSteps) != 0 {
		return out, fmt.Errorf("the hostile instruction was not refused before signing")
	}

	order, quote, err := newOrder(ctx, rows, account, wallet, request)
	if err != nil {
		return out, err
	}
	out.Order = order.ID.String()
	payload, _ := json.Marshal(legit)
	if err := rows.SaveFunding(ctx, order.ID, string(payload)); err != nil {
		return out, err
	}
	result, sendErr := custody.Send(ctx, order, wallet, quote, legit, in)
	if result != nil {
		out.TxHash = result.Hash
	}
	if sendErr != nil || result == nil || result.Hash == "" {
		return out, fmt.Errorf("legitimate funding not sent: %v", sendErr)
	}
	deadline := time.Now().Add(wait)
	for {
		state, err := custody.SettleFunding(ctx, order, wallet)
		out.Funding = map[swap.FundingState]string{swap.FundingUnknown: "unknown", swap.FundingIncluded: "included", swap.FundingFailed: "failed"}[state]
		out.Elapsed = time.Since(started).Round(time.Second).String()
		if err == nil && state != swap.FundingUnknown {
			if state == swap.FundingFailed {
				return out, fmt.Errorf("funding failed on chain")
			}
			return out, nil
		}
		if time.Now().After(deadline) {
			return out, fmt.Errorf("funding not included within %s", wait)
		}
		time.Sleep(5 * time.Second)
	}
}

// instructions returns the hostile and the legitimate funding for a mode.
func instructions(ctx context.Context, mode string, network *models.Chain, wallet *models.Wallet, registry *chain.Registry, request swap.QuoteRequest, to string) (*swap.Funding, *swap.Funding, error) {
	switch mode {
	case "evm":
		target := wrappedNative[network.ID]
		if target == "" || request.From.NetworkID == 0 {
			return nil, nil, fmt.Errorf("no wrapped native contract for %s", network.ID)
		}
		tokens := registry.TokensForChain(network.ID)
		if len(tokens) == 0 {
			return nil, nil, fmt.Errorf("no catalog token on %s for the hostile call", network.ID)
		}
		// The audit's hostile call: transfer the wallet's catalog token to a third
		// party, built by a "provider" as the swap funding.
		attacker := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
		data := append([]byte{0xa9, 0x05, 0x9c, 0xbb}, common.LeftPadBytes(attacker.Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(big.NewInt(1).Bytes(), 32)...)
		hostile := &swap.Funding{Kind: "evm", ChainID: request.From.NetworkID, To: tokens[0].Contract, AmountRaw: request.AmountRaw, Data: "0x" + hex.EncodeToString(data)}
		legit := &swap.Funding{Kind: "evm", ChainID: request.From.NetworkID, To: target, AmountRaw: request.AmountRaw, Data: "0xd0e30db0"}
		return hostile, legit, nil
	case "memo":
		if to == "" {
			return nil, nil, fmt.Errorf("--to is required")
		}
		hostile := &swap.Funding{Kind: "transfer", To: to, AmountRaw: request.AmountRaw, Memo: "=:ETH.ETH:0x000000000000000000000000000000000000dEaD:0"}
		legit := &swap.Funding{Kind: "transfer", To: to, AmountRaw: request.AmountRaw, Memo: "=:ETH.ETH:" + request.ToAddress + ":0"}
		return hostile, legit, nil
	case "sol":
		if to == "" {
			return nil, nil, fmt.Errorf("--to is required")
		}
		lamports, ok := new(big.Int).SetString(request.AmountRaw, 10)
		if !ok || !lamports.IsUint64() {
			return nil, nil, fmt.Errorf("invalid lamports")
		}
		endpoint := solanaEndpoint()
		blockhash, err := rpc.New(endpoint).GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
		if err != nil {
			return nil, nil, fmt.Errorf("latest blockhash: %w", err)
		}
		payer := solana.MustPublicKeyFromBase58(request.FromAddress)
		recipient := solana.MustPublicKeyFromBase58(to)
		build := func(program solana.PublicKey) (string, error) {
			transfer := system.NewTransferInstruction(lamports.Uint64(), payer, recipient).Build()
			instruction := solana.NewInstruction(program, transfer.Accounts(), mustData(transfer))
			tx, err := solana.NewTransaction([]solana.Instruction{instruction}, blockhash.Value.Blockhash, solana.TransactionPayer(payer))
			if err != nil {
				return "", err
			}
			return tx.ToBase64()
		}
		// Hostile: the same accounts handed to an unknown program, which would
		// receive the wallet's signature.
		hostileTx, err := build(solana.MustPublicKeyFromBase58("Stake11111111111111111111111111111111111111"))
		if err != nil {
			return nil, nil, err
		}
		legitTx, err := build(solana.SystemProgramID)
		if err != nil {
			return nil, nil, err
		}
		return &swap.Funding{Kind: "solana", Data: hostileTx}, &swap.Funding{Kind: "solana", Data: legitTx}, nil
	}
	return nil, nil, fmt.Errorf("unknown mode %q", mode)
}

func mustData(instruction solana.Instruction) []byte {
	data, err := instruction.Data()
	if err != nil {
		panic(err)
	}
	return data
}

// solanaEndpoint is the devnet RPC the harness reads a blockhash from.
func solanaEndpoint() string {
	if url := os.Getenv("SWAP_E2E_SOLANA_RPC"); url != "" {
		return url
	}
	return rpc.DevNet_RPC
}

func newOrder(ctx context.Context, rows *repositories.SwapRepository, account *models.Account, wallet *models.Wallet, request swap.QuoteRequest) (*models.SwapOrder, *swap.Quote, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, nil, err
	}
	quote := &swap.Quote{ID: uuid.New(), Provider: swap.ProviderSwapKit, RouteID: "controlled-testnet-" + uuid.NewString(), Request: request, ExpectedRaw: "1", MinimumRaw: "1", ExpiresAt: time.Now().UTC().Add(2 * time.Minute), Payload: json.RawMessage(`{}`)}
	row := &models.SwapQuote{ID: quote.ID, AccountID: account.ID, SourceWalletID: wallet.ID, DestinationWalletID: wallet.ID, Provider: quote.Provider, RouteID: quote.RouteID, RequestPayload: string(encoded), ExpectedRaw: "1", MinimumRaw: "1", FeesPayload: "[]", AttemptsPayload: `[{"provider":"controlled","outcome":"selected"}]`, RoutePayload: "{}", ExpiresAt: quote.ExpiresAt}
	if err := rows.CreateQuote(ctx, row); err != nil {
		return nil, nil, err
	}
	order := &models.SwapOrder{ID: uuid.New(), AccountID: account.ID, QuoteID: quote.ID, IdempotencyKey: uuid.New(), SourceWalletID: wallet.ID, DestinationWalletID: wallet.ID, Provider: quote.Provider, Status: swap.StatusPreparing, FundingPayload: "{}", ReceivedRaw: "0", RefundRaw: "0", AuthorizedUntil: time.Now().Add(5 * time.Minute)}
	if err := rows.CreateOrder(ctx, order); err != nil {
		return nil, nil, err
	}
	return order, quote, nil
}
