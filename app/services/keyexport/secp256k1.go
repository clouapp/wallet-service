package keyexport

import (
	"bytes"
	"crypto/subtle"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/hdkey"
)

const maxNonHardenedIndex = int64(1) << 31

// secp256k1Keys exports the wallet key for genesis rows and wallet key + BIP-32 tweak
// for bip32 rows: the same math sweep and withdraw sign with (hdkey), so the
// exported key is the one that controls the row's address.
func (s *Service) secp256k1Keys(plan WalletPlan, secrets *walletSecrets) ([]AddressKey, error) {
	walletPublicKey, err := decodeHexField(plan.Wallet.MPCPublicKey, hdkey.CompressedPublicKeySize)
	if err != nil {
		return nil, refuse("wallet public key is not a %d-byte compressed secp256k1 key", hdkey.CompressedPublicKeySize)
	}
	walletKey, err := s.deps.MPC.ReconstructSecp256k1PrivateKey(secrets.shareA, secrets.shareB)
	if err != nil {
		return nil, refuse("secp256k1 key could not be reconstructed from the two shares")
	}
	defer zeroBytes(walletKey)
	derived, err := hdkey.PublicKeyOf(walletKey)
	if err != nil || subtle.ConstantTimeCompare(derived, walletPublicKey) != 1 {
		return nil, refuse("reconstructed secp256k1 key does not match the wallet public key")
	}
	chainCode, _ := decodeHexField(plan.Wallet.MPCChainCode, hdkey.ChainCodeSize)

	keys := make([]AddressKey, 0, len(plan.Addresses))
	for _, address := range plan.Addresses {
		key, err := secp256k1AddressKey(plan.Network, address, walletKey, walletPublicKey, chainCode)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func secp256k1AddressKey(network Network, address models.Address, walletKey, walletPublicKey, chainCode []byte) (AddressKey, error) {
	privateKey, publicKey, source, err := secp256k1KeyOf(address, walletKey, walletPublicKey, chainCode)
	if err != nil {
		return AddressKey{}, err
	}
	defer zeroBytes(privateKey)
	addressNet, err := matchSecp256k1Address(network, publicKey, address)
	if err != nil {
		return AddressKey{}, err
	}
	key := newAddressKey(address, addressNet, publicKey, KeyKindSecp256k1, source)

	switch network.AdapterType {
	case models.AdapterTypeEVM:
		privateKeyHex, err := EVMPrivateKeyHex(privateKey)
		if err != nil {
			return AddressKey{}, refuse("address %s: key could not be encoded", address.Address)
		}
		verified, err := EVMAddressOfPrivateKeyHex(privateKeyHex)
		if err != nil || !strings.EqualFold(verified, address.Address) {
			return AddressKey{}, refuse("address %s: exported EVM key does not re-derive the address", address.Address)
		}
		key.EVM, key.VerifiedAddress = &EVMKey{PrivateKeyHex: privateKeyHex}, verified
	case models.AdapterTypeBitcoin:
		utxoKey, err := NewUTXOKey(privateKey, network.ChainID, addressNet.testnet)
		if err != nil {
			return AddressKey{}, refuse("address %s: key could not be encoded as WIF", address.Address)
		}
		verified, err := UTXOP2WPKHAddressOfWIF(utxoKey.WIF, network.ChainID, addressNet.testnet)
		if err != nil || verified != address.Address {
			return AddressKey{}, refuse("address %s: exported WIF does not re-derive the address", address.Address)
		}
		if models.IsLitecoinChainID(network.ChainID) {
			key.Litecoin = utxoKey
		} else {
			key.Bitcoin = utxoKey
		}
		key.VerifiedAddress = verified
	case models.AdapterTypeTron:
		tronKey, err := NewTronKey(privateKey)
		if err != nil {
			return AddressKey{}, refuse("address %s: key could not be encoded", address.Address)
		}
		verified, err := TronAddressOfPrivateKeyHex(tronKey.PrivateKeyHex)
		if err != nil || verified != address.Address {
			return AddressKey{}, refuse("address %s: exported TRON key does not re-derive the address", address.Address)
		}
		key.Tron, key.VerifiedAddress = tronKey, verified
	default:
		return AddressKey{}, refuse("adapter %q has no secp256k1 export format", network.AdapterType)
	}
	key.Notes = append(key.Notes, addressNet.notes...)
	return key, nil
}

// secp256k1KeyOf returns the private key (caller zeroes it) and public key of a row.
func secp256k1KeyOf(address models.Address, walletKey, walletPublicKey, chainCode []byte) ([]byte, []byte, string, error) {
	switch address.DerivationType {
	case DerivationGenesis:
		return bytes.Clone(walletKey), walletPublicKey, KeySourceWalletKey, nil
	case DerivationBIP32:
		if len(chainCode) != hdkey.ChainCodeSize {
			return nil, nil, "", refuse("address %s is a bip32 child but the wallet has no valid chain code", address.Address)
		}
		if address.DerivationIndex < 0 || int64(address.DerivationIndex) >= maxNonHardenedIndex {
			return nil, nil, "", refuse("address %s has an out-of-range derivation index %d", address.Address, address.DerivationIndex)
		}
		child, err := hdkey.DeriveSecp256k1Child(walletPublicKey, chainCode, uint32(address.DerivationIndex))
		if err != nil {
			return nil, nil, "", refuse("address %s: bip32 child %d could not be derived", address.Address, address.DerivationIndex)
		}
		childKey, err := hdkey.ChildPrivateKey(walletKey, child.Tweak)
		if err != nil {
			return nil, nil, "", refuse("address %s: bip32 child key could not be computed", address.Address)
		}
		derived, err := hdkey.PublicKeyOf(childKey)
		if err != nil || subtle.ConstantTimeCompare(derived, child.PublicKey) != 1 {
			zeroBytes(childKey)
			return nil, nil, "", refuse("address %s: bip32 child key does not match the child public key", address.Address)
		}
		return childKey, child.PublicKey, KeySourceBIP32Child, nil
	default:
		return nil, nil, "", refuse("address %s has derivation type %q, which secp256k1 wallets do not use", address.Address, address.DerivationType)
	}
}

type addressNetwork struct {
	name    string
	testnet bool
	notes   []string
}

const otherUTXONetworkNote = "endereço pertence a outra rede (mainnet/testnet) que a configurada para a chain (provavelmente retirado após troca de rede); a WIF foi gerada para a rede do endereço"

// matchSecp256k1Address checks that publicKey owns the row's address. A Bitcoin or
// Litecoin row may predate a network switch (bc1 retired for tb1, ltc1 for tltc1, or
// the reverse): its key is exported for the network its address belongs to, with a
// note. TRON addresses are the same on every TRON network.
func matchSecp256k1Address(network Network, publicKey []byte, address models.Address) (addressNetwork, error) {
	primary := addressNetwork{name: networkLabel(network.Name), testnet: network.Testnet}
	switch network.AdapterType {
	case models.AdapterTypeEVM:
		derived, err := addressing.DeriveEthAddress(publicKey)
		if err == nil && strings.EqualFold(derived, address.Address) {
			return primary, nil
		}
	case models.AdapterTypeTron:
		derived, err := addressing.DeriveTronAddress(publicKey)
		if err == nil && derived == address.Address {
			return primary, nil
		}
	case models.AdapterTypeBitcoin:
		for _, testnet := range []bool{network.Testnet, !network.Testnet} {
			derived, err := addressing.DeriveBtcAddress(addressing.UTXOHRP(network.ChainID, testnet), publicKey)
			if err != nil || derived != address.Address {
				continue
			}
			if testnet == network.Testnet {
				return primary, nil
			}
			return addressNetwork{
				name:    utxoNetworkName(network.ChainID, testnet),
				testnet: testnet,
				notes:   []string{otherUTXONetworkNote},
			}, nil
		}
	}
	return addressNetwork{}, refuse("address %s (%s, index %d) is not controlled by the reconstructed key", address.Address, address.DerivationType, address.DerivationIndex)
}

const unknownNetworkLabel = "unknown"

func networkLabel(name string) string {
	if name == "" {
		return unknownNetworkLabel
	}
	return name
}
