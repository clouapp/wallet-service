// Package keyexport reconstructs, from both MPC shares, the private keys that control
// every address of a wallet, checks each one against the address stored in the
// database, and packs them with both shares into a password-protected AES-256 zip.
// Nothing here prints key material; callers only ever see ids, labels, chains,
// addresses and counts.
package keyexport

import (
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// FormatVersion identifies the layout of the files inside the archive.
const FormatVersion = 1

// Derivation types an address row can carry (see the addresses migration).
const (
	DerivationGenesis  = "genesis"
	DerivationBIP32    = "bip32"
	DerivationSLIP0010 = "slip0010"
)

// Key kinds written to wallet.json.
const (
	KeyKindSecp256k1     = "secp256k1-private-key"
	KeyKindEd25519Seed   = "ed25519-rfc8032-seed"
	KeyKindEd25519Scalar = "ed25519-raw-scalar"
)

// Where an exported key came from.
const (
	KeySourceWalletKey     = "mpc-shares"
	KeySourceBIP32Child    = "mpc-shares+bip32-tweak"
	KeySourceSLIP0010      = "mpc-shares+slip0010"
	KeySourceStoredSeed    = "address-row-seed-decrypted-with-passphrase"
	KeySourceEd25519Shamir = "mpc-shares-lagrange"
)

// Network is where a wallet's chain record points. Name is "" when the record
// names no known network.
type Network struct {
	ChainID     string
	AdapterType string
	Name        string
	Testnet     bool
}

// WalletPlan is a wallet selected for export with everything public that is needed
// to reconstruct and verify its keys.
type WalletPlan struct {
	Wallet    models.Wallet
	Network   Network
	Addresses []models.Address
}

// RunsOnMainnet reports whether the chain record points at a production network.
func (p WalletPlan) RunsOnMainnet() bool {
	return !p.Network.Testnet
}

// SpansEVMNetworks reports whether the wallet's keys also control the same address
// on every other EVM network, mainnets included.
func (p WalletPlan) SpansEVMNetworks() bool {
	return p.Network.AdapterType == models.AdapterTypeEVM
}

// SpansTronNetworks reports whether the wallet's keys control the same T... address
// on TRON mainnet and every TRON testnet (and, as 0x + the same 20 bytes, on every
// EVM network).
func (p WalletPlan) SpansTronNetworks() bool {
	return p.Network.AdapterType == models.AdapterTypeTron
}

// Refusal is a wallet that was not exported. Reason never carries key material.
type Refusal struct {
	WalletID uuid.UUID `json:"wallet_id"`
	Label    string    `json:"label"`
	Chain    string    `json:"chain"`
	Reason   string    `json:"reason"`
}

// WalletKeys is wallet.json: the wallet, its network and one entry per address.
type WalletKeys struct {
	FormatVersion int          `json:"format_version"`
	WalletID      string       `json:"wallet_id"`
	Label         string       `json:"label"`
	Chain         string       `json:"chain"`
	Status        string       `json:"status"`
	Curve         string       `json:"curve"`
	Network       NetworkInfo  `json:"network"`
	PublicKeyHex  string       `json:"wallet_public_key_hex"`
	ChainCodeHex  string       `json:"chain_code_hex,omitempty"`
	Shares        SharesInfo   `json:"mpc_shares"`
	Addresses     []AddressKey `json:"addresses"`
	Warnings      []string     `json:"warnings,omitempty"`
}

type NetworkInfo struct {
	Name        string `json:"name"`
	Testnet     bool   `json:"testnet"`
	Kind        string `json:"kind"`
	AdapterType string `json:"adapter_type"`
}

type SharesInfo struct {
	ShareAFile  string `json:"share_a_file"`
	ShareBFile  string `json:"share_b_file"`
	Format      string `json:"format"`
	Description string `json:"description"`
}

// AddressKey is one database address and the key that controls it, in the formats
// wallets import. Exactly one of EVM, Bitcoin, Litecoin, Tron, Solana or
// SolanaScalar is set.
type AddressKey struct {
	AddressID       string `json:"address_id"`
	Address         string `json:"address"`
	DerivationType  string `json:"derivation_type"`
	DerivationIndex int    `json:"derivation_index"`
	IsActive        bool   `json:"is_active"`
	Label           string `json:"label,omitempty"`
	ExternalUserID  string `json:"external_user_id,omitempty"`
	Network         string `json:"network"`
	Testnet         bool   `json:"testnet"`
	PublicKeyHex    string `json:"public_key_hex"`
	KeyKind         string `json:"key_kind"`
	KeySource       string `json:"key_source"`
	// VerifiedAddress is the address re-derived from the exported key itself.
	VerifiedAddress string `json:"verified_address"`

	EVM          *EVMKey          `json:"evm,omitempty"`
	Bitcoin      *BitcoinKey      `json:"bitcoin,omitempty"`
	Litecoin     *BitcoinKey      `json:"litecoin,omitempty"`
	Tron         *TronKey         `json:"tron,omitempty"`
	Solana       *SolanaKey       `json:"solana,omitempty"`
	SolanaScalar *SolanaScalarKey `json:"solana_scalar,omitempty"`
	Notes        []string         `json:"notes,omitempty"`
}

type EVMKey struct {
	PrivateKeyHex string `json:"private_key_hex"`
}

// BitcoinKey is the key of a Bitcoin-family address (Bitcoin or Litecoin): the WIF
// carries the network's prefix.
type BitcoinKey struct {
	WIF                    string `json:"wif_compressed"`
	ElectrumImport         string `json:"electrum_import"`
	Descriptor             string `json:"descriptor"`
	DescriptorWithChecksum string `json:"descriptor_with_checksum"`
	PrivateKeyHex          string `json:"private_key_hex"`
}

// TronKey is the key TronLink imports and the hex (41...) form of the address.
type TronKey struct {
	PrivateKeyHex string `json:"private_key_hex"`
	AddressHex    string `json:"address_hex"`
}

type SolanaKey struct {
	SeedHex       string `json:"seed_hex"`
	KeypairBase58 string `json:"keypair_base58"`
	KeypairJSON   []int  `json:"keypair_json_bytes"`
	KeypairFile   string `json:"keypair_file"`
}

type SolanaScalarKey struct {
	ScalarHexBigEndian    string `json:"scalar_hex_big_endian"`
	ScalarHexLittleEndian string `json:"scalar_hex_little_endian"`
	ImportableInPhantom   bool   `json:"importable_in_phantom_or_solana_keygen"`
}
