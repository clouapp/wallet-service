package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/models"
)

const (
	exportKeysTestWalletA = "01e5e921-9443-4718-bf60-d624e65b03d9"
	exportKeysTestWalletB = "536e5579-bb17-4e45-aa97-13a1dd60af1f"
)

type exportKeysWalletStub struct{}

func (exportKeysWalletStub) FindAll(context.Context) ([]models.Wallet, error) { return nil, nil }
func (exportKeysWalletStub) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, nil
}

type exportKeysAddressStub struct{}

func (exportKeysAddressStub) FindByWalletID(context.Context, uuid.UUID) ([]models.Address, error) {
	return nil, nil
}

type exportKeysChainStub struct{}

func (exportKeysChainStub) FindByID(context.Context, string) (*models.Chain, error) { return nil, nil }

func TestNew_Wallets_ExportKeysKeepsItsDependencies(t *testing.T) {
	wallets := exportKeysWalletStub{}
	addresses := exportKeysAddressStub{}
	chains := exportKeysChainStub{}
	cmd := NewWalletsExportKeys(WalletsExportKeysDeps{Wallets: wallets, Addresses: addresses, Chains: chains})
	if cmd == nil {
		t.Fatal("NewWalletsExportKeys returned nil")
	}
	if cmd.wallets != wallets {
		t.Fatal("export keys did not keep the wallet source")
	}
	if cmd.addresses != addresses {
		t.Fatal("export keys did not keep the address source")
	}
	if cmd.chains != chains {
		t.Fatal("export keys did not keep the chain source")
	}
}

func TestNew_Wallets_ExportKeysStoresNilDependencies(t *testing.T) {
	cmd := NewWalletsExportKeys(WalletsExportKeysDeps{})
	if cmd == nil {
		t.Fatal("NewWalletsExportKeys returned nil")
	}
	if cmd.wallets != nil || cmd.addresses != nil || cmd.chains != nil {
		t.Fatal("omitted dependencies should stay nil")
	}
	if _, err := cmd.newWalletsExportService(); err == nil || !strings.Contains(err.Error(), "wallets, addresses, chains and share B are required") {
		t.Fatalf("nil dependencies must be refused before secrets are touched: %v", err)
	}
}

func TestWallets_ExportKeys_SignatureAndFlags(t *testing.T) {
	cmd := &WalletsExportKeys{}
	if cmd.Signature() != "wallets:export-keys" {
		t.Fatalf("signature %s", cmd.Signature())
	}
	flags := map[string]command.Flag{}
	for _, flag := range cmd.Extend().Flags {
		switch typed := flag.(type) {
		case *command.StringSliceFlag:
			flags[typed.Name] = flag
		case *command.StringFlag:
			flags[typed.Name] = flag
		case *command.BoolFlag:
			flags[typed.Name] = flag
		default:
			t.Fatalf("unexpected flag type %T", flag)
		}
	}
	if _, ok := flags["wallet"].(*command.StringSliceFlag); !ok {
		t.Fatal("--wallet must be repeatable (StringSliceFlag)")
	}
	for _, name := range []string{"out", "passphrase-vault", "allow-production"} {
		if flags[name] == nil {
			t.Fatalf("missing --%s", name)
		}
	}
	for name := range flags {
		if strings.Contains(name, "password") || name == "passphrase" || name == "passphrase-stdin" {
			t.Fatalf("secrets must never come from flags, found --%s", name)
		}
	}
}

func TestParse_WalletsExportKeysFlags_AcceptsRepeatedAndCommaSeparatedUUIDs(t *testing.T) {
	invocation, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{
		Wallets: []string{exportKeysTestWalletA, " " + exportKeysTestWalletB + "," + exportKeysTestWalletA},
		Out:     " /tmp/x/keys.zip ", PassphraseVault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(invocation.walletIDs) != 2 || invocation.walletIDs[0].String() != exportKeysTestWalletA || invocation.walletIDs[1].String() != exportKeysTestWalletB {
		t.Fatalf("wallet ids %v", invocation.walletIDs)
	}
	if invocation.out != "/tmp/x/keys.zip" || !invocation.passphraseVault || invocation.allowProduction {
		t.Fatalf("invocation %+v", invocation)
	}
	all, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{All: true})
	if err != nil || len(all.walletIDs) != 0 {
		t.Fatalf("--all means every wallet: %+v %v", all, err)
	}
}

func TestParse_WalletsExportKeysFlags_RequiresAnExplicitSelection(t *testing.T) {
	if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{}); err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("no --wallet and no --all must be refused: %v", err)
	}
	if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{All: true, Wallets: []string{exportKeysTestWalletA}}); err == nil {
		t.Fatal("--wallet together with --all must be refused")
	}
}

func TestParse_WalletsExportKeysFlags_RefusesBadWalletIDs(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", uuid.Nil.String(), exportKeysTestWalletA + ",", "base_deposit"} {
		if _, err := parseWalletsExportKeysFlags(walletsExportKeysFlags{Wallets: []string{bad}}); err == nil {
			t.Fatalf("--wallet %q must be refused", bad)
		}
	}
}
