package keyexport

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
)

const (
	slip0010HardenedOffset = uint32(0x80000000)
	slip0010KeySize        = 32
	solanaKeypairDir       = "solana-keygen"
)

const genesisScalarNote = "chave genesis de uma wallet MPC ed25519: é um ESCALAR bruto (a·B = chave pública), não uma seed RFC 8032; Phantom e solana-keygen não importam. Veja INSTRUCOES.md, seção Solana / chave genesis."

// ed25519Keys exports the Lagrange-interpolated scalar for the genesis row (what
// sweep signs the base address with) and, for slip0010 rows, the RFC 8032 seed that
// controls the child: the SLIP-0010 child of the share sum, or the seed stored
// encrypted on the row, whichever owns the address.
func (s *Service) ed25519Keys(plan WalletPlan, secrets *walletSecrets) ([]AddressKey, []ArchiveFile, error) {
	walletPublicKey, err := decodeHexField(plan.Wallet.MPCPublicKey, ed25519.PublicKeySize)
	if err != nil {
		return nil, nil, refuse("wallet public key is not a %d-byte ed25519 key", ed25519.PublicKeySize)
	}
	scalar, err := s.deps.MPC.ReconstructEd25519Scalar(secrets.shareA, secrets.shareB)
	if err != nil {
		return nil, nil, refuse("ed25519 scalar could not be reconstructed from the two shares")
	}
	defer zeroBytes(scalar)
	derived, err := Ed25519PublicKeyOfScalar(scalar)
	if err != nil || subtle.ConstantTimeCompare(derived, walletPublicKey) != 1 {
		return nil, nil, refuse("reconstructed ed25519 scalar does not match the wallet public key")
	}
	children := &ed25519ChildSeeds{
		mpc: s.deps.MPC, secrets: secrets,
		chainCode: decodeOptionalChainCode(plan.Wallet.MPCChainCode),
	}
	defer children.wipe()

	keys := make([]AddressKey, 0, len(plan.Addresses))
	var files []ArchiveFile
	usedFiles := map[string]bool{}
	for _, address := range plan.Addresses {
		switch address.DerivationType {
		case DerivationGenesis:
			key, err := ed25519GenesisKey(plan.Network, address, scalar, walletPublicKey)
			if err != nil {
				return nil, nil, err
			}
			keys = append(keys, key)
		case DerivationSLIP0010:
			key, file, err := children.addressKey(plan, address, usedFiles)
			if err != nil {
				return nil, nil, err
			}
			keys, files = append(keys, key), append(files, file)
		default:
			return nil, nil, refuse("address %s has derivation type %q, which ed25519 wallets do not use", address.Address, address.DerivationType)
		}
	}
	return keys, files, nil
}

func ed25519GenesisKey(network Network, address models.Address, scalar, walletPublicKey []byte) (AddressKey, error) {
	owned, err := addressing.DeriveSolAddress(walletPublicKey)
	if err != nil || owned != address.Address {
		return AddressKey{}, refuse("address %s (genesis) is not the address of the wallet public key", address.Address)
	}
	scalarKey, err := NewSolanaScalarKey(scalar)
	if err != nil {
		return AddressKey{}, refuse("address %s: scalar could not be encoded", address.Address)
	}
	verified, err := SolanaAddressOfScalarHex(scalarKey.ScalarHexBigEndian)
	if err != nil || verified != address.Address {
		return AddressKey{}, refuse("address %s: exported scalar does not re-derive the address", address.Address)
	}
	key := newAddressKey(address, addressNetwork{name: networkLabel(network.Name), testnet: network.Testnet}, walletPublicKey, KeyKindEd25519Scalar, KeySourceEd25519Shamir)
	key.SolanaScalar, key.VerifiedAddress = scalarKey, verified
	key.Notes = append(key.Notes, genesisScalarNote)
	return key, nil
}

// ed25519ChildSeeds resolves child seeds; the share-sum master is reconstructed
// once, on first use, and zeroed by wipe.
type ed25519ChildSeeds struct {
	mpc       Reconstructor
	secrets   *walletSecrets
	chainCode []byte
	master    []byte
	masterErr error
	loaded    bool
}

func (c *ed25519ChildSeeds) wipe() { zeroBytes(c.master) }

func (c *ed25519ChildSeeds) masterKey() ([]byte, error) {
	if !c.loaded {
		c.loaded = true
		c.master, c.masterErr = c.mpc.ReconstructEd25519PrivateKey(c.secrets.shareA, c.secrets.shareB)
	}
	return c.master, c.masterErr
}

func (c *ed25519ChildSeeds) addressKey(plan WalletPlan, address models.Address, usedFiles map[string]bool) (AddressKey, ArchiveFile, error) {
	seed, source, err := c.seedFor(address)
	if err != nil {
		return AddressKey{}, ArchiveFile{}, err
	}
	defer zeroBytes(seed)
	fileName := keypairFileName(address, usedFiles)
	solanaKey, err := NewSolanaKey(seed, fileName)
	if err != nil {
		return AddressKey{}, ArchiveFile{}, refuse("address %s: seed could not be encoded", address.Address)
	}
	fromBase58, errBase58 := SolanaAddressOfKeypairBase58(solanaKey.KeypairBase58)
	fromJSON, errJSON := SolanaAddressOfKeypairJSON(solanaKey.KeypairJSON)
	if errBase58 != nil || errJSON != nil || fromBase58 != address.Address || fromJSON != address.Address {
		return AddressKey{}, ArchiveFile{}, refuse("address %s: exported keypair does not re-derive the address", address.Address)
	}
	publicKey := ed25519.NewKeyFromSeed(seed)
	defer zeroBytes(publicKey)
	key := newAddressKey(address, addressNetwork{name: networkLabel(plan.Network.Name), testnet: plan.Network.Testnet},
		publicKey[ed25519.SeedSize:], KeyKindEd25519Seed, source)
	key.Solana, key.VerifiedAddress = solanaKey, fromBase58
	if source == KeySourceStoredSeed {
		key.Notes = append(key.Notes, "a seed SLIP-0010 re-derivada das shares não controla este endereço; exportada a seed gravada (cifrada com a passphrase) na linha do endereço, que é a que o sweep usa para assinar")
	}
	file, err := solanaKeypairFile(fileName, solanaKey.KeypairJSON)
	if err != nil {
		return AddressKey{}, ArchiveFile{}, err
	}
	return key, file, nil
}

// seedFor returns a seed (caller zeroes it) whose public key is the row's address.
func (c *ed25519ChildSeeds) seedFor(address models.Address) ([]byte, string, error) {
	if address.DerivationIndex < 0 || int64(address.DerivationIndex) >= maxNonHardenedIndex {
		return nil, "", refuse("address %s has an out-of-range derivation index %d", address.Address, address.DerivationIndex)
	}
	if master, err := c.masterKey(); err == nil && len(c.chainCode) == slip0010KeySize {
		derived, err := SLIP0010Ed25519Child(master, c.chainCode, uint32(address.DerivationIndex))
		if err == nil && seedControls(derived, address.Address) {
			return derived, KeySourceSLIP0010, nil
		}
		zeroBytes(derived)
	}
	stored, err := decryptStoredSeed(address, c.secrets.passphrase)
	if err == nil && seedControls(stored, address.Address) {
		return stored, KeySourceStoredSeed, nil
	}
	zeroBytes(stored)
	return nil, "", refuse("address %s (slip0010, index %d): neither the SLIP-0010 seed of the shares nor the seed stored on the row controls it", address.Address, address.DerivationIndex)
}

// SLIP0010Ed25519Child is the hardened SLIP-0010 child the wallet service issues
// ed25519 addresses with: HMAC-SHA512(chainCode, 0x00 || master || ser32(index + 2^31))[:32].
// The caller must zero the result.
func SLIP0010Ed25519Child(master, chainCode []byte, index uint32) ([]byte, error) {
	if len(master) != slip0010KeySize || len(chainCode) != slip0010KeySize {
		return nil, fmt.Errorf("slip0010 master key and chain code must be %d bytes", slip0010KeySize)
	}
	if index >= slip0010HardenedOffset {
		return nil, fmt.Errorf("slip0010 index %d is already hardened", index)
	}
	data := make([]byte, 1+slip0010KeySize+4)
	defer zeroBytes(data)
	copy(data[1:], master)
	binary.BigEndian.PutUint32(data[1+slip0010KeySize:], slip0010HardenedOffset+index)
	mac := hmac.New(sha512.New, chainCode)
	mac.Write(data)
	digest := mac.Sum(nil)
	defer zeroBytes(digest)
	child := make([]byte, slip0010KeySize)
	copy(child, digest[:slip0010KeySize])
	return child, nil
}

func seedControls(seed []byte, address string) bool {
	if len(seed) != ed25519.SeedSize {
		return false
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	defer zeroBytes(privateKey)
	derived, err := addressing.DeriveSolAddress(privateKey.Public().(ed25519.PublicKey))
	return err == nil && derived == address
}

func decryptStoredSeed(address models.Address, passphrase string) ([]byte, error) {
	if address.EncryptedPrivateKey == "" || address.EncryptionIV == "" || address.EncryptionSalt == "" || passphrase == "" {
		return nil, fmt.Errorf("no stored seed")
	}
	ciphertext, errCiphertext := hex.DecodeString(address.EncryptedPrivateKey)
	iv, errIV := hex.DecodeString(address.EncryptionIV)
	salt, errSalt := hex.DecodeString(address.EncryptionSalt)
	if errCiphertext != nil || errIV != nil || errSalt != nil {
		return nil, fmt.Errorf("stored seed is not hex")
	}
	return mpcpkg.DecryptShare(&mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: iv, Salt: salt}, passphrase)
}

func keypairFileName(address models.Address, used map[string]bool) string {
	base := fmt.Sprintf("%s/index-%d", solanaKeypairDir, address.DerivationIndex)
	name := base + ".json"
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s-%d.json", base, n)
	}
	used[name] = true
	return name
}
