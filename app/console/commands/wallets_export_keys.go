package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/keyexport"
)

const (
	walletsExportKeysInterruptedExit = 130
	walletsExportKeysGitTimeout      = 5 * time.Second
	walletsExportKeysWalletSeparator = ","
)

// WalletsExportKeys writes, per wallet, the private keys reconstructed from both MPC
// shares (checked against every database address) and the two shares into an
// AES-256 zip. Passwords are only typed on the terminal; nothing secret is printed.
type WalletsExportKeys struct{}

type walletsExportKeysFlags struct {
	Wallets         []string
	All             bool
	Out             string
	PassphraseVault bool
	AllowProduction bool
}

type walletsExportKeysInvocation struct {
	walletIDs       []uuid.UUID
	out             string
	passphraseVault bool
	allowProduction bool
}

func (c *WalletsExportKeys) Signature() string {
	return "wallets:export-keys"
}

func (c *WalletsExportKeys) Description() string {
	return "Export the selected wallets' private keys and both MPC shares into an AES-256 encrypted zip (interactive)"
}

func (c *WalletsExportKeys) Extend() command.Extend {
	return command.Extend{
		Category: "wallets",
		Flags: []command.Flag{
			&command.StringSliceFlag{Name: "wallet", Usage: "wallet UUID to export (repeatable); required unless --all"},
			&command.BoolFlag{Name: "all", Usage: "export every wallet in the database (instead of --wallet)"},
			&command.StringFlag{Name: "out", Usage: "zip path outside the repository (default: ~/" + keyexport.DefaultExportSubdir + "/wallet-keys-<UTC>.zip)"},
			&command.BoolFlag{Name: "passphrase-vault", Usage: "read each wallet passphrase from the encrypted e2e wallet vault instead of prompting"},
			&command.BoolFlag{Name: "allow-production", Usage: "allow APP_ENV=production (a confirmation phrase is still asked on the terminal)"},
		},
	}
}

func (c *WalletsExportKeys) Handle(ctx console.Context) error {
	invocation, err := parseWalletsExportKeysFlags(readWalletsExportKeysFlags(ctx))
	if err != nil {
		return failCommand(ctx, err)
	}
	tty, err := keyexport.OpenTTY()
	if err != nil {
		return failCommand(ctx, err)
	}
	defer tty.Close()

	var output atomic.Pointer[keyexport.ArchiveOutput]
	stopSignals := cleanUpOnSignal(tty, &output)
	defer stopSignals()

	if err := keyexport.RequireEnvironmentAllowed(facades.Config().GetString("app.env"), invocation.allowProduction, tty); err != nil {
		return failCommand(ctx, err)
	}
	now := time.Now()
	outPath, err := prepareWalletsExportPath(invocation.out, now)
	if err != nil {
		return failCommand(ctx, err)
	}
	return runWalletsExport(ctx, invocation, tty, keyexport.NewArchiveOutput(outPath), &output, now)
}

func runWalletsExport(ctx console.Context, invocation walletsExportKeysInvocation, tty *keyexport.TTY, archive *keyexport.ArchiveOutput, output *atomic.Pointer[keyexport.ArchiveOutput], now time.Time) error {
	service, err := newWalletsExportService()
	if err != nil {
		return failCommand(ctx, err)
	}
	wallets, err := service.SelectWallets(invocation.walletIDs)
	if err != nil {
		return failCommand(ctx, err)
	}
	plans, refused, err := service.Plan(wallets)
	if err != nil {
		return failCommand(ctx, err)
	}
	reportWalletsExportPlan(ctx, plans, refused, archive.Path())
	if len(plans) == 0 {
		return failCommand(ctx, errors.New("no selected wallet can be exported"))
	}

	password, err := keyexport.ReadArchivePassword(tty)
	if err != nil {
		return failCommand(ctx, err)
	}
	defer zeroPassword(password)

	result, err := service.Export(context.Background(), plans, walletsExportPassphrases(invocation, tty))
	if err != nil {
		return failCommand(ctx, err)
	}
	defer result.Wipe()
	result.Refused = append(refused, result.Refused...)
	if len(result.Wallets) == 0 {
		reportWalletsExportRefusals(ctx, result.Refused)
		return failCommand(ctx, errors.New("no wallet passed reconstruction; nothing was written"))
	}

	files, err := keyexport.ArchiveFiles(result, keyexport.ArchiveMeta{GeneratedAt: now, AppEnv: facades.Config().GetString("app.env")})
	if err != nil {
		return failCommand(ctx, err)
	}
	output.Store(archive)
	if err := archive.Write(func(w io.Writer) error {
		return keyexport.WriteEncryptedZip(w, password, files, now)
	}); err != nil {
		return failCommand(ctx, err)
	}
	reportWalletsExportResult(ctx, result, archive.Path())
	if len(result.Refused) > 0 {
		return failCommand(ctx, fmt.Errorf("%d wallet(s) were refused and are not in the archive", len(result.Refused)))
	}
	return nil
}

func readWalletsExportKeysFlags(ctx console.Context) walletsExportKeysFlags {
	return walletsExportKeysFlags{
		Wallets: ctx.OptionSlice("wallet"), All: ctx.OptionBool("all"), Out: ctx.Option("out"),
		PassphraseVault: ctx.OptionBool("passphrase-vault"), AllowProduction: ctx.OptionBool("allow-production"),
	}
}

// parseWalletsExportKeysFlags validates every flag before the terminal, the database
// or any secret is touched.
func parseWalletsExportKeysFlags(flags walletsExportKeysFlags) (walletsExportKeysInvocation, error) {
	invocation := walletsExportKeysInvocation{
		out: strings.TrimSpace(flags.Out), passphraseVault: flags.PassphraseVault, allowProduction: flags.AllowProduction,
	}
	seen := map[uuid.UUID]bool{}
	for _, value := range flags.Wallets {
		for _, raw := range strings.Split(value, walletsExportKeysWalletSeparator) {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return walletsExportKeysInvocation{}, errors.New("--wallet must not be empty")
			}
			id, err := uuid.Parse(trimmed)
			if err != nil || id == uuid.Nil {
				return walletsExportKeysInvocation{}, fmt.Errorf("--wallet %q is not a wallet UUID", trimmed)
			}
			if !seen[id] {
				seen[id] = true
				invocation.walletIDs = append(invocation.walletIDs, id)
			}
		}
	}
	if flags.All && len(invocation.walletIDs) > 0 {
		return walletsExportKeysInvocation{}, errors.New("use either --wallet or --all, not both")
	}
	if !flags.All && len(invocation.walletIDs) == 0 {
		return walletsExportKeysInvocation{}, errors.New("choose the wallets with --wallet UUID (repeatable), or pass --all to export every wallet")
	}
	return invocation, nil
}

func prepareWalletsExportPath(requested string, now time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil && requested == "" {
		return "", fmt.Errorf("home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	return keyexport.PrepareOutputPath(keyexport.OutputRequest{
		Requested: requested, Home: home, Now: now,
		ForbiddenRoots: keyexport.RepositoryRoots(cwd, gitToplevel),
	})
}

func gitToplevel(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), walletsExportKeysGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// cleanUpOnSignal restores terminal echo and removes a half-written archive when the
// operator interrupts, then exits.
func cleanUpOnSignal(tty *keyexport.TTY, output *atomic.Pointer[keyexport.ArchiveOutput]) func() {
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case <-signals:
			tty.Restore()
			if archive := output.Load(); archive != nil {
				archive.RemoveIfPartial()
			}
			os.Exit(walletsExportKeysInterruptedExit)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}

func newWalletsExportService() (*keyexport.Service, error) {
	ctr := container.Get()
	if ctr.SecretsManager == nil {
		return nil, errors.New("secrets manager is not configured; share B cannot be fetched")
	}
	return keyexport.NewService(keyexport.Dependencies{
		Wallets:   ctr.WalletRepo,
		Addresses: ctr.AddressRepo,
		Networks:  chainNetworkResolver{chains: ctr.ChainRepo},
		ShareB:    secretsManagerShareB{secrets: ctr.SecretsManager},
		MPC:       ctr.MPCService,
	})
}

func walletsExportPassphrases(invocation walletsExportKeysInvocation, tty keyexport.Terminal) keyexport.PassphraseSource {
	if !invocation.passphraseVault {
		return keyexport.PromptPassphrases{Terminal: tty}
	}
	return keyexport.VaultPassphrases{Read: func(ctx context.Context, walletID uuid.UUID) (string, error) {
		vault, err := evmcall.DefaultVaultPassphrase()
		if err != nil {
			return "", err
		}
		return vault.Read(ctx, walletID)
	}}
}

// chainNetworkResolver classifies a chain record like the wallet API does; the RPC
// URL is decrypted only for adapters whose network it names and never leaves here.
type chainNetworkResolver struct {
	chains repositories.ChainRepository
}

func (r chainNetworkResolver) ResolveNetwork(chainID string) (keyexport.Network, error) {
	if r.chains == nil {
		return keyexport.Network{}, errors.New("chain repository is not configured")
	}
	record, err := r.chains.FindByID(chainID)
	if err != nil || record == nil {
		return keyexport.Network{}, fmt.Errorf("chain record %s not found", chainID)
	}
	resolved := record.ResolveNetwork(networkRPCURLOf(record))
	return keyexport.Network{ChainID: record.ID, AdapterType: record.AdapterType, Name: resolved.Name, Testnet: resolved.Testnet}, nil
}

func networkRPCURLOf(record *models.Chain) string {
	if record.AdapterType != models.AdapterTypeSolana && record.AdapterType != models.AdapterTypeBitcoin {
		return ""
	}
	stored, err := facades.Crypt().DecryptString(record.RpcURL)
	if err != nil {
		return ""
	}
	rpcURL, err := models.ResolveRPCURL(stored)
	if err != nil {
		return ""
	}
	return rpcURL
}

type secretValueGetter interface {
	GetSecretValue(ctx context.Context, input *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type secretsManagerShareB struct {
	secrets secretValueGetter
}

func (s secretsManagerShareB) FetchShareB(ctx context.Context, wallet models.Wallet) ([]byte, error) {
	if s.secrets == nil {
		return nil, errors.New("secrets manager is not configured")
	}
	arn := strings.TrimSpace(wallet.MPCSecretARN)
	if arn == "" {
		return nil, fmt.Errorf("wallet %s has no MPC secret ARN", wallet.ID)
	}
	out, err := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &arn})
	if err != nil {
		return nil, fmt.Errorf("fetch share B of wallet %s", wallet.ID)
	}
	return out.SecretBinary, nil
}

func reportWalletsExportPlan(ctx console.Context, plans []keyexport.WalletPlan, refused []keyexport.Refusal, outPath string) {
	ctx.Info(fmt.Sprintf("%d wallet(s) selected for export to %s", len(plans), outPath))
	for _, plan := range plans {
		ctx.Line(fmt.Sprintf("  %s  %-24s chain=%-10s network=%s testnet=%t addresses=%d",
			plan.Wallet.ID, plan.Wallet.Label, plan.Wallet.Chain, displayNetwork(plan.Network.Name), plan.Network.Testnet, len(plan.Addresses)))
	}
	for _, plan := range plans {
		if plan.RunsOnMainnet() {
			ctx.Warning(fmt.Sprintf("MAINNET: wallet %s (%s, %s) points at %s — its keys control real funds", plan.Wallet.ID, plan.Wallet.Label, plan.Wallet.Chain, displayNetwork(plan.Network.Name)))
		}
	}
	for _, plan := range plans {
		if plan.SpansEVMNetworks() {
			ctx.Warning(fmt.Sprintf("EVM: wallet %s (%s) — the same addresses exist on every EVM network, mainnets included; the exported keys spend funds there too", plan.Wallet.ID, plan.Wallet.Label))
		}
		if plan.SpansTronNetworks() {
			ctx.Warning(fmt.Sprintf("TRON: wallet %s (%s) — the same T... addresses exist on TRON mainnet and every TRON testnet, and the keys also control the matching 0x address on every EVM network", plan.Wallet.ID, plan.Wallet.Label))
		}
	}
	reportWalletsExportRefusals(ctx, refused)
}

func reportWalletsExportRefusals(ctx console.Context, refused []keyexport.Refusal) {
	for _, refusal := range refused {
		ctx.Warning(fmt.Sprintf("REFUSED wallet %s (%s, %s): %s", refusal.WalletID, refusal.Label, refusal.Chain, refusal.Reason))
	}
}

func reportWalletsExportResult(ctx console.Context, result *keyexport.Result, outPath string) {
	addresses := 0
	for _, wallet := range result.Wallets {
		addresses += wallet.AddressCount
		ctx.Line(fmt.Sprintf("  exported %s  %-24s chain=%-10s addresses=%d (all re-derived and matched)",
			wallet.Plan.Wallet.ID, wallet.Plan.Wallet.Label, wallet.Plan.Wallet.Chain, wallet.AddressCount))
	}
	reportWalletsExportRefusals(ctx, result.Refused)
	ctx.Info(fmt.Sprintf("wrote %s (mode 0600): %d wallet(s), %d address key(s), %d refused", outPath, len(result.Wallets), addresses, len(result.Refused)))
}

func displayNetwork(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}

func zeroPassword(password []byte) {
	for i := range password {
		password[i] = 0
	}
}
