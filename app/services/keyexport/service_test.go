package keyexport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"
	"time"

	"filippo.io/edwards25519"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/mr-tron/base58"

	bitcoinchain "github.com/macrowallets/waas/app/adapters/chain/bitcoin"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
)

// exportAndOpen runs the whole pipeline (select → plan → export → zip) and opens the
// zip with the archive password, the way an operator would.
func exportAndOpen(t *testing.T, store *fakeStore, passphrases PassphraseSource) (*Result, map[string][]byte) {
	t.Helper()
	service := store.service(t)
	wallets, err := service.SelectWallets(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	plans, refused, err := service.Plan(context.Background(), wallets)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Export(context.Background(), plans, passphrases)
	if err != nil {
		t.Fatal(err)
	}
	result.Refused = append(refused, result.Refused...)
	t.Cleanup(result.Wipe)
	if len(result.Wallets) == 0 {
		return result, nil
	}
	files, err := ArchiveFiles(result, ArchiveMeta{GeneratedAt: time.Unix(1_800_000_000, 0), AppEnv: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := WriteEncryptedZip(&archive, []byte(testArchivePassword), files, time.Unix(1_800_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	contents, err := ReadEncryptedZip(bytes.NewReader(archive.Bytes()), int64(archive.Len()), []byte(testArchivePassword))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEncryptedZip(bytes.NewReader(archive.Bytes()), int64(archive.Len()), []byte(testArchivePassword+"x")); err == nil {
		t.Fatal("a wrong archive password must not open the zip")
	}
	return result, contents
}

func walletDocument(t *testing.T, contents map[string][]byte, walletID uuid.UUID) WalletKeys {
	t.Helper()
	raw, ok := contents[path.Join(walletDir(walletID.String()), walletFileName)]
	if !ok {
		t.Fatalf("wallet.json of %s missing", walletID)
	}
	var document WalletKeys
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func requireSharesInArchive(t *testing.T, contents map[string][]byte, fixture walletFixture) {
	t.Helper()
	dir := walletDir(fixture.wallet.ID.String())
	if !bytes.Equal(contents[path.Join(dir, shareAFileName)], fixture.shareA) {
		t.Fatal("share_a.json must be the decrypted share A")
	}
	if !bytes.Equal(contents[path.Join(dir, shareBFileName)], fixture.shareB) {
		t.Fatal("share_b.json must be share B")
	}
}

func requireAllAddressesExported(t *testing.T, document WalletKeys, fixture walletFixture) {
	t.Helper()
	if len(document.Addresses) != len(fixture.addresses) {
		t.Fatalf("exported %d addresses, wallet has %d", len(document.Addresses), len(fixture.addresses))
	}
	for i, key := range document.Addresses {
		if key.Address != fixture.addresses[i].Address || key.VerifiedAddress != key.Address {
			t.Fatalf("address %d: exported %s verified %s, want %s", i, key.Address, key.VerifiedAddress, fixture.addresses[i].Address)
		}
	}
}

func TestExport_Secp2561_KeysControlEveryEVMAndBitcoinAddress(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	evm := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, baseSepolia, "base_deposit", 2)
	btc := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, bitcoinTest, "btc_deposit", 2)
	if !strings.HasPrefix(btc.addresses[1].Address, "tb1") || btc.addresses[1].DerivationType != DerivationBIP32 {
		t.Fatalf("fixture child %+v", btc.addresses[1])
	}

	result, contents := exportAndOpen(t, newFakeStore(evm, btc), fixedPassphrase(testWalletPassphrase))
	if len(result.Wallets) != 2 || len(result.Refused) != 0 {
		t.Fatalf("exported %d refused %+v", len(result.Wallets), result.Refused)
	}

	evmDocument := walletDocument(t, contents, evm.wallet.ID)
	requireAllAddressesExported(t, evmDocument, evm)
	requireSharesInArchive(t, contents, evm)
	for _, key := range evmDocument.Addresses {
		if key.EVM == nil || key.Bitcoin != nil {
			t.Fatalf("%s: want only an EVM key", key.Address)
		}
		ecdsaKey, err := crypto.HexToECDSA(strings.TrimPrefix(key.EVM.PrivateKeyHex, "0x"))
		if err != nil {
			t.Fatal(err)
		}
		if derived := crypto.PubkeyToAddress(ecdsaKey.PublicKey).Hex(); !strings.EqualFold(derived, key.Address) {
			t.Fatalf("EVM key derives %s, row is %s", derived, key.Address)
		}
	}
	if evmDocument.Addresses[0].PublicKeyHex != evm.wallet.MPCPublicKey || evmDocument.Addresses[1].KeySource != KeySourceBIP32Child {
		t.Fatal("genesis must carry the wallet public key and children the bip32 source")
	}

	btcDocument := walletDocument(t, contents, btc.wallet.ID)
	requireAllAddressesExported(t, btcDocument, btc)
	for _, key := range btcDocument.Addresses {
		wif, err := btcutil.DecodeWIF(key.Bitcoin.WIF)
		if err != nil || !wif.IsForNet(&chaincfg.TestNet3Params) || !wif.CompressPubKey {
			t.Fatalf("%s: WIF must be compressed testnet (%v)", key.Address, err)
		}
		witness, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(wif.SerializePubKey()), &chaincfg.TestNet3Params)
		if err != nil || witness.EncodeAddress() != key.Address {
			t.Fatalf("WIF derives %v, row is %s", witness, key.Address)
		}
		if key.Bitcoin.ElectrumImport != "p2wpkh:"+key.Bitcoin.WIF || !strings.HasPrefix(key.Bitcoin.DescriptorWithChecksum, "wpkh("+key.Bitcoin.WIF+")#") {
			t.Fatalf("%s: electrum/descriptor forms are wrong", key.Address)
		}
	}
	if _, ok := contents[instructionsFileName]; !ok {
		t.Fatal("INSTRUCOES.md missing")
	}
	var manifest Manifest
	if err := json.Unmarshal(contents[manifestFileName], &manifest); err != nil || len(manifest.Wallets) != 2 || !manifest.Wallets[0].AllEVMChains {
		t.Fatalf("manifest %+v err %v", manifest, err)
	}
}

func TestExport_Bitcoin_MainnetUsesMainnetWIF(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	btc := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, bitcoinMain, "btc_main", 1)
	_, contents := exportAndOpen(t, newFakeStore(btc), fixedPassphrase(testWalletPassphrase))
	document := walletDocument(t, contents, btc.wallet.ID)
	if document.Network.Testnet || document.Network.Kind != networkKindMainnet {
		t.Fatalf("network %+v", document.Network)
	}
	for _, key := range document.Addresses {
		wif, err := btcutil.DecodeWIF(key.Bitcoin.WIF)
		if err != nil || !wif.IsForNet(&chaincfg.MainNetParams) || !strings.HasPrefix(key.Address, "bc1") {
			t.Fatalf("%s: mainnet WIF expected", key.Address)
		}
	}
}

func TestExport_Bitcoin_RowOfTheOtherNetworkGetsThatNetworksWIF(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	btc := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, bitcoinMain, "btc_switched", 0)
	btc.network = bitcoinTest
	_, contents := exportAndOpen(t, newFakeStore(btc), fixedPassphrase(testWalletPassphrase))
	key := walletDocument(t, contents, btc.wallet.ID).Addresses[0]
	wif, err := btcutil.DecodeWIF(key.Bitcoin.WIF)
	if err != nil || !wif.IsForNet(&chaincfg.MainNetParams) || key.Testnet || len(key.Notes) == 0 {
		t.Fatalf("a retired bc1 row must get a mainnet WIF and a note: %+v", key.Notes)
	}
}

func TestExport_LitecoinKeysAreLitecoinWIFsOfEveryAddress(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	testnet := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, litecoinTest, "ltc_deposit", 2)
	mainnet := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, litecoinMain, "ltc_main", 1)
	if !strings.HasPrefix(testnet.addresses[1].Address, "tltc1q") || testnet.addresses[1].DerivationType != DerivationBIP32 {
		t.Fatalf("fixture child %+v", testnet.addresses[1])
	}

	result, contents := exportAndOpen(t, newFakeStore(testnet, mainnet), fixedPassphrase(testWalletPassphrase))
	if len(result.Wallets) != 2 || len(result.Refused) != 0 {
		t.Fatalf("exported %d refused %+v", len(result.Wallets), result.Refused)
	}
	for _, fixture := range []walletFixture{testnet, mainnet} {
		document := walletDocument(t, contents, fixture.wallet.ID)
		requireAllAddressesExported(t, document, fixture)
		requireSharesInArchive(t, contents, fixture)
		params := bitcoinchain.BitcoinFamilyParams(models.ChainLTC, fixture.network.Testnet)
		for _, key := range document.Addresses {
			if key.Litecoin == nil || key.Bitcoin != nil || key.Network != fixture.network.Name {
				t.Fatalf("%s: want only a litecoin key on %s, got %+v", key.Address, fixture.network.Name, key)
			}
			wif, err := btcutil.DecodeWIF(key.Litecoin.WIF)
			if err != nil || !wif.IsForNet(params) || !wif.CompressPubKey {
				t.Fatalf("%s: WIF must be compressed for %s (%v)", key.Address, params.Name, err)
			}
			witness, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(wif.SerializePubKey()), params)
			if err != nil || witness.EncodeAddress() != key.Address {
				t.Fatalf("WIF derives %v, row is %s", witness, key.Address)
			}
			if key.Litecoin.ElectrumImport != "p2wpkh:"+key.Litecoin.WIF {
				t.Fatalf("%s: electrum-ltc form is wrong", key.Address)
			}
		}
	}
	if mainnetKey := walletDocument(t, contents, mainnet.wallet.ID).Addresses[0]; !strings.HasPrefix(mainnetKey.Address, "ltc1q") || !strings.HasPrefix(mainnetKey.Litecoin.WIF, "T") {
		t.Fatalf("mainnet litecoin row %s must get a T... WIF", mainnetKey.Address)
	}
	instructions := string(contents[instructionsFileName])
	if !strings.Contains(instructions, "## Litecoin") || !strings.Contains(instructions, "importprivkey") || !strings.Contains(instructions, "electrum-ltc --testnet") {
		t.Fatal("INSTRUCOES.md must explain how to import Litecoin keys")
	}
}

func TestExport_LitecoinRowOfTheOtherNetworkGetsThatNetworksWIF(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	ltc := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, litecoinMain, "ltc_switched", 0)
	ltc.network = litecoinTest
	_, contents := exportAndOpen(t, newFakeStore(ltc), fixedPassphrase(testWalletPassphrase))
	key := walletDocument(t, contents, ltc.wallet.ID).Addresses[0]
	wif, err := btcutil.DecodeWIF(key.Litecoin.WIF)
	if err != nil || !wif.IsForNet(bitcoinchain.BitcoinFamilyParams(models.ChainLTC, false)) || key.Testnet || key.Network != models.NetworkLitecoinMainnet || len(key.Notes) == 0 {
		t.Fatalf("a retired ltc1 row must get a mainnet litecoin WIF and a note: %+v %+v", key.Network, key.Notes)
	}
}

func TestExport_TronKeysImportIntoTronLinkForEveryAddress(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	tron := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, tronNile, "tron_deposit", 2)
	if !addressing.IsTronAddress(tron.addresses[2].Address) || tron.addresses[2].DerivationType != DerivationBIP32 {
		t.Fatalf("fixture child %+v", tron.addresses[2])
	}

	result, contents := exportAndOpen(t, newFakeStore(tron), fixedPassphrase(testWalletPassphrase))
	if len(result.Wallets) != 1 || len(result.Refused) != 0 {
		t.Fatalf("exported %d refused %+v", len(result.Wallets), result.Refused)
	}
	document := walletDocument(t, contents, tron.wallet.ID)
	requireAllAddressesExported(t, document, tron)
	requireSharesInArchive(t, contents, tron)
	for _, key := range document.Addresses {
		if key.Tron == nil || key.EVM != nil || key.Bitcoin != nil || len(key.Tron.PrivateKeyHex) != 64 || strings.HasPrefix(key.Tron.PrivateKeyHex, "0x") {
			t.Fatalf("%s: want only a 64-hex TRON key", key.Address)
		}
		ecdsaKey, err := crypto.HexToECDSA(key.Tron.PrivateKeyHex)
		if err != nil {
			t.Fatal(err)
		}
		derived, err := addressing.DeriveTronAddress(crypto.CompressPubkey(&ecdsaKey.PublicKey))
		if err != nil || derived != key.Address {
			t.Fatalf("TRON key derives %s, row is %s", derived, key.Address)
		}
		if addressHex, err := addressing.TronAddressToHex(key.Address); err != nil || addressHex != key.Tron.AddressHex {
			t.Fatalf("%s: address hex %s, want %s", key.Address, key.Tron.AddressHex, addressHex)
		}
	}
	if document.Addresses[1].KeySource != KeySourceBIP32Child || len(document.Warnings) == 0 || !strings.Contains(strings.Join(document.Warnings, " "), "TRON") {
		t.Fatalf("children must carry the bip32 source and the wallet a TRON warning: %+v", document.Warnings)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents[manifestFileName], &manifest); err != nil || len(manifest.Wallets) != 1 || !manifest.Wallets[0].AllTronChains || manifest.Wallets[0].AllEVMChains {
		t.Fatalf("manifest %+v err %v", manifest, err)
	}
	if instructions := string(contents[instructionsFileName]); !strings.Contains(instructions, "## TRON") || !strings.Contains(instructions, "Import private key") {
		t.Fatal("INSTRUCOES.md must explain how to import TRON keys")
	}
}

func TestPlan_AcceptsTronOnlyOnSecp256k1(t *testing.T) {
	if err := requireCurveMatchesAdapter(mpcpkg.CurveSecp256k1, models.AdapterTypeTron); err != nil {
		t.Fatalf("secp256k1 TRON wallets must be exportable: %v", err)
	}
	if err := requireCurveMatchesAdapter(mpcpkg.CurveEd25519, models.AdapterTypeTron); err == nil {
		t.Fatal("an ed25519 TRON wallet must be refused")
	}
}

func TestExport_Ed25519_GenesisIsAScalarAndChildrenAreImportableSeeds(t *testing.T) {
	keys := mustEd25519Keys(t)
	sol := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "sol_deposit", 2)
	result, contents := exportAndOpen(t, newFakeStore(sol), fixedPassphrase(testWalletPassphrase))
	if len(result.Wallets) != 1 || len(result.Refused) != 0 {
		t.Fatalf("exported %d refused %+v", len(result.Wallets), result.Refused)
	}
	document := walletDocument(t, contents, sol.wallet.ID)
	requireAllAddressesExported(t, document, sol)
	requireSharesInArchive(t, contents, sol)

	genesis := document.Addresses[0]
	if genesis.SolanaScalar == nil || genesis.Solana != nil || genesis.KeyKind != KeyKindEd25519Scalar || genesis.SolanaScalar.ImportableInPhantom {
		t.Fatalf("genesis must be exported as a non-importable scalar: %+v", genesis)
	}
	littleEndian, _ := hex.DecodeString(genesis.SolanaScalar.ScalarHexLittleEndian)
	scalar, err := edwards25519.NewScalar().SetCanonicalBytes(littleEndian)
	if err != nil {
		t.Fatal(err)
	}
	if base58.Encode(new(edwards25519.Point).ScalarBaseMult(scalar).Bytes()) != genesis.Address {
		t.Fatal("scalar·B must be the genesis address")
	}

	for _, child := range document.Addresses[1:] {
		if child.Solana == nil || child.KeyKind != KeyKindEd25519Seed || child.KeySource != KeySourceSLIP0010 {
			t.Fatalf("child %s must be an importable SLIP-0010 seed: %+v", child.Address, child)
		}
		secret, err := base58.Decode(child.Solana.KeypairBase58)
		if err != nil || len(secret) != ed25519.PrivateKeySize {
			t.Fatal("phantom secret must be 64 bytes base58")
		}
		publicKey := ed25519.NewKeyFromSeed(secret[:ed25519.SeedSize]).Public().(ed25519.PublicKey)
		if base58.Encode(publicKey) != child.Address {
			t.Fatalf("seed derives %s, row is %s", base58.Encode(publicKey), child.Address)
		}
		var fileBytes []int
		if err := json.Unmarshal(contents[path.Join(walletDir(sol.wallet.ID.String()), child.Solana.KeypairFile)], &fileBytes); err != nil {
			t.Fatal(err)
		}
		if address, err := SolanaAddressOfKeypairJSON(fileBytes); err != nil || address != child.Address {
			t.Fatalf("solana-keygen file derives %s %v", address, err)
		}
	}
}

func TestExport_Ed25519_ChildFallsBackToTheSeedStoredOnTheRow(t *testing.T) {
	keys := mustEd25519Keys(t)
	sol := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "sol_legacy", 1)
	sol.wallet.MPCChainCode = ""
	_, contents := exportAndOpen(t, newFakeStore(sol), fixedPassphrase(testWalletPassphrase))
	child := walletDocument(t, contents, sol.wallet.ID).Addresses[1]
	if child.KeySource != KeySourceStoredSeed || child.VerifiedAddress != sol.addresses[1].Address {
		t.Fatalf("child source %s verified %s", child.KeySource, child.VerifiedAddress)
	}
}

func TestExport_Refuses_AWalletWhoseRowIsNotControlledByTheKey(t *testing.T) {
	keys := mustSecp256k1Keys(t)
	good := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, baseSepolia, "good", 1)
	bad := newWalletFixture(t, keys, mpcpkg.CurveSecp256k1, bscMainnet, "bad", 1)
	bad.addresses[1].DerivationIndex += 7

	result, contents := exportAndOpen(t, newFakeStore(good, bad), fixedPassphrase(testWalletPassphrase))
	if len(result.Wallets) != 1 || result.Wallets[0].Plan.Wallet.ID != good.wallet.ID {
		t.Fatalf("only the good wallet must be exported")
	}
	if len(result.Refused) != 1 || result.Refused[0].WalletID != bad.wallet.ID || !strings.Contains(result.Refused[0].Reason, bad.addresses[1].Address) {
		t.Fatalf("refusal %+v", result.Refused)
	}
	for name := range contents {
		if strings.Contains(name, bad.wallet.ID.String()) {
			t.Fatalf("refused wallet leaked into the archive: %s", name)
		}
	}
	requireNoSecretIn(t, result.Refused[0].Reason, bad)
}

func TestExport_Refuses_SharesThatDoNotBelongToTheWallet(t *testing.T) {
	sol := newWalletFixture(t, mustEd25519Keys(t), mpcpkg.CurveEd25519, solanaDevnet, "sol", 0)
	sol.shareB = mustEd25519Keys(t).ShareB
	result, _ := exportAndOpen(t, newFakeStore(sol), fixedPassphrase(testWalletPassphrase))
	if len(result.Wallets) != 0 || len(result.Refused) != 1 {
		t.Fatalf("a wallet with a foreign share B must be refused: %+v", result.Refused)
	}
	requireNoSecretIn(t, result.Refused[0].Reason, sol)
}

func TestExport_Refuses_AWrongPassphrase(t *testing.T) {
	sol := newWalletFixture(t, mustEd25519Keys(t), mpcpkg.CurveEd25519, solanaDevnet, "sol", 0)
	result, _ := exportAndOpen(t, newFakeStore(sol), fixedPassphrase("not-the-passphrase-123"))
	if len(result.Wallets) != 0 || len(result.Refused) != 1 || !strings.Contains(result.Refused[0].Reason, "share A") {
		t.Fatalf("refusal %+v", result.Refused)
	}
}

func TestExport_Prompt_RetriesAWrongPassphraseAndAbortStopsTheExport(t *testing.T) {
	sol := newWalletFixture(t, mustEd25519Keys(t), mpcpkg.CurveEd25519, solanaDevnet, "sol", 0)
	terminal := &scriptedTerminal{secrets: [][]byte{[]byte("wrong-passphrase-1"), []byte(testWalletPassphrase)}}
	result, _ := exportAndOpen(t, newFakeStore(sol), PromptPassphrases{Terminal: terminal})
	if len(result.Wallets) != 1 || len(terminal.notices) != 1 {
		t.Fatalf("second attempt must unlock the wallet: exported %d notices %v", len(result.Wallets), terminal.notices)
	}

	store := newFakeStore(sol)
	service := store.service(t)
	plans, _, err := service.Plan(context.Background(), store.wallets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Export(context.Background(), plans, PromptPassphrases{Terminal: &scriptedTerminal{}}); !errors.Is(err, ErrAborted) {
		t.Fatalf("a terminal that goes away must abort the export, got %v", err)
	}
}

func TestSelect_Wallets_FiltersByIDAndFailsOnUnknownIDs(t *testing.T) {
	keys := mustEd25519Keys(t)
	first := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "a", 0)
	second := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "b", 0)
	service := newFakeStore(first, second).service(t)

	selected, err := service.SelectWallets(context.Background(), []uuid.UUID{second.wallet.ID, second.wallet.ID})
	if err != nil || len(selected) != 1 || selected[0].ID != second.wallet.ID {
		t.Fatalf("selected %+v err %v", selected, err)
	}
	unknown := uuid.New()
	if _, err := service.SelectWallets(context.Background(), []uuid.UUID{first.wallet.ID, unknown}); err == nil || !strings.Contains(err.Error(), unknown.String()) {
		t.Fatalf("unknown id must fail fast, got %v", err)
	}
	if _, err := service.SelectWallets(context.Background(), []uuid.UUID{uuid.Nil}); err == nil {
		t.Fatal("the nil UUID must be refused")
	}
}

func TestPlan_Refuses_PublicInconsistenciesBeforeTouchingSecrets(t *testing.T) {
	keys := mustEd25519Keys(t)
	noAddresses := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "empty", 0)
	noAddresses.addresses = nil
	wrongCurve := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "curve", 0)
	wrongCurve.wallet.MPCCurve = string(mpcpkg.CurveSecp256k1)
	unknownChain := newWalletFixture(t, keys, mpcpkg.CurveEd25519, solanaDevnet, "chain", 0)
	unknownChain.wallet.Chain = "nochain"

	store := newFakeStore(noAddresses, wrongCurve, unknownChain)
	delete(store.byChain, unknownChain.wallet.Chain)
	plans, refused, err := store.service(t).Plan(context.Background(), store.wallets)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 || len(refused) != 3 {
		t.Fatalf("plans %d refused %+v", len(plans), refused)
	}
}

func TestNew_Service_RequiresEveryDependency(t *testing.T) {
	if _, err := NewService(Dependencies{}); err == nil {
		t.Fatal("missing dependencies must be refused")
	}
}

// requireNoSecretIn checks a printable reason quotes no share or key material.
func requireNoSecretIn(t *testing.T, reason string, fixture walletFixture) {
	t.Helper()
	for _, share := range [][]byte{fixture.shareA, fixture.shareB} {
		var parsed struct {
			Xi *json.Number `json:"Xi"`
		}
		if err := json.Unmarshal(share, &parsed); err == nil && parsed.Xi != nil && strings.Contains(reason, parsed.Xi.String()) {
			t.Fatal("reason quotes share material")
		}
	}
	if strings.Contains(reason, testWalletPassphrase) {
		t.Fatal("reason quotes the passphrase")
	}
}
