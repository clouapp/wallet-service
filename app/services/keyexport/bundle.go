package keyexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

const (
	walletsDir           = "wallets"
	walletFileName       = "wallet.json"
	shareAFileName       = "share_a.json"
	shareBFileName       = "share_b.json"
	manifestFileName     = "manifest.json"
	instructionsFileName = "INSTRUCOES.md"
	sharesFormat         = "tss-lib v2 LocalPartySaveData (JSON); secp256k1: ecdsa/keygen, ed25519: eddsa/keygen"
	sharesDescription    = "share_a.json é a share do cliente já DECIFRADA (no banco ela fica cifrada com Argon2id+AES-256-GCM pela passphrase); share_b.json é a share do serviço (AWS Secrets Manager). As duas juntas reconstroem todas as chaves desta wallet."
	timestampLayout      = time.RFC3339
	jsonIndent           = "  "
)

const (
	networkKindTestnet = "testnet"
	networkKindMainnet = "mainnet"
)

// ArchiveFile is one entry of the archive; Data is zeroed by Result.Wipe.
type ArchiveFile struct {
	Name string
	Data []byte
}

// ArchiveMeta is the public context recorded in the manifest.
type ArchiveMeta struct {
	GeneratedAt time.Time
	AppEnv      string
}

// Manifest is manifest.json: what the archive holds, without key material.
type Manifest struct {
	FormatVersion int              `json:"format_version"`
	GeneratedAt   string           `json:"generated_at"`
	AppEnv        string           `json:"app_env"`
	Encryption    string           `json:"encryption"`
	Wallets       []ManifestWallet `json:"wallets"`
	Refused       []Refusal        `json:"refused_wallets"`
}

type ManifestWallet struct {
	WalletID     string `json:"wallet_id"`
	Label        string `json:"label"`
	Chain        string `json:"chain"`
	Status       string `json:"status"`
	Curve        string `json:"curve"`
	Network      string `json:"network"`
	Testnet      bool   `json:"testnet"`
	AllEVMChains bool   `json:"key_valid_on_every_evm_network"`
	AddressCount int    `json:"address_count"`
	Directory    string `json:"directory"`
}

const archiveEncryptionLabel = "WinZip AES-256 (AE-2)"

// ArchiveFiles lists every entry of the archive: instructions, manifest, then each
// wallet's directory. The wallet buffers stay owned by result.
func ArchiveFiles(result *Result, meta ArchiveMeta) ([]ArchiveFile, error) {
	if result == nil || len(result.Wallets) == 0 {
		return nil, fmt.Errorf("nothing to archive: no wallet was exported")
	}
	manifest := buildManifest(result, meta)
	encodedManifest, err := json.MarshalIndent(manifest, "", jsonIndent)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	files := []ArchiveFile{
		{Name: instructionsFileName, Data: []byte(RenderInstructions(manifest))},
		{Name: manifestFileName, Data: encodedManifest},
	}
	for _, wallet := range result.Wallets {
		files = append(files, wallet.files...)
	}
	return files, nil
}

func buildManifest(result *Result, meta ArchiveMeta) Manifest {
	manifest := Manifest{
		FormatVersion: FormatVersion,
		GeneratedAt:   meta.GeneratedAt.UTC().Format(timestampLayout),
		AppEnv:        meta.AppEnv,
		Encryption:    archiveEncryptionLabel,
		Wallets:       make([]ManifestWallet, 0, len(result.Wallets)),
		Refused:       append([]Refusal{}, result.Refused...),
	}
	for _, wallet := range result.Wallets {
		plan := wallet.Plan
		manifest.Wallets = append(manifest.Wallets, ManifestWallet{
			WalletID:     plan.Wallet.ID.String(),
			Label:        plan.Wallet.Label,
			Chain:        plan.Wallet.Chain,
			Status:       plan.Wallet.Status,
			Curve:        plan.Wallet.MPCCurve,
			Network:      networkLabel(plan.Network.Name),
			Testnet:      plan.Network.Testnet,
			AllEVMChains: plan.SpansEVMNetworks(),
			AddressCount: wallet.AddressCount,
			Directory:    walletDir(plan.Wallet.ID.String()) + "/",
		})
	}
	return manifest
}

func walletDir(walletID string) string {
	return path.Join(walletsDir, walletID)
}

func renderWalletFiles(plan WalletPlan, keys []AddressKey, secrets *walletSecrets, extra []ArchiveFile) ([]ArchiveFile, error) {
	dir := walletDir(plan.Wallet.ID.String())
	document := WalletKeys{
		FormatVersion: FormatVersion,
		WalletID:      plan.Wallet.ID.String(),
		Label:         plan.Wallet.Label,
		Chain:         plan.Wallet.Chain,
		Status:        plan.Wallet.Status,
		Curve:         plan.Wallet.MPCCurve,
		Network: NetworkInfo{
			Name: networkLabel(plan.Network.Name), Testnet: plan.Network.Testnet,
			Kind: networkKind(plan.Network.Testnet), AdapterType: plan.Network.AdapterType,
		},
		PublicKeyHex: plan.Wallet.MPCPublicKey,
		ChainCodeHex: plan.Wallet.MPCChainCode,
		Shares: SharesInfo{
			ShareAFile: shareAFileName, ShareBFile: shareBFileName,
			Format: sharesFormat, Description: sharesDescription,
		},
		Addresses: keys,
		Warnings:  walletWarnings(plan),
	}
	encoded, err := json.MarshalIndent(document, "", jsonIndent)
	if err != nil {
		return nil, refuse("wallet.json could not be encoded")
	}
	files := []ArchiveFile{
		{Name: path.Join(dir, walletFileName), Data: encoded},
		{Name: path.Join(dir, shareAFileName), Data: bytes.Clone(secrets.shareA)},
		{Name: path.Join(dir, shareBFileName), Data: bytes.Clone(secrets.shareB)},
	}
	for _, file := range extra {
		files = append(files, ArchiveFile{Name: path.Join(dir, file.Name), Data: file.Data})
	}
	return files, nil
}

func walletWarnings(plan WalletPlan) []string {
	var warnings []string
	if plan.RunsOnMainnet() {
		warnings = append(warnings, "a chain desta wallet está configurada para MAINNET: estas chaves controlam fundos reais")
	}
	if plan.SpansEVMNetworks() {
		warnings = append(warnings, "chave EVM: o mesmo endereço existe em TODAS as redes EVM (Ethereum, Polygon, BSC, Base, Arbitrum, mainnets incluídas); a chave gasta fundos em qualquer uma delas")
	}
	if plan.Wallet.Status != "" && plan.Wallet.Status != string(types.WalletStatusActive) {
		warnings = append(warnings, fmt.Sprintf("wallet com status %q na plataforma", plan.Wallet.Status))
	}
	return warnings
}

func networkKind(testnet bool) string {
	if testnet {
		return networkKindTestnet
	}
	return networkKindMainnet
}

// solanaKeypairFile is the solana-keygen keypair file: a JSON array of 64 bytes.
func solanaKeypairFile(name string, keypair []int) (ArchiveFile, error) {
	encoded, err := json.Marshal(keypair)
	if err != nil {
		return ArchiveFile{}, refuse("solana keypair file could not be encoded")
	}
	return ArchiveFile{Name: name, Data: encoded}, nil
}
