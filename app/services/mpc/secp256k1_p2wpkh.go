package mpc

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// bitcoinSighashAll is the SIGHASH_ALL byte appended to a P2WPKH witness signature.
const bitcoinSighashAll = 0x01

// SignSecp256k1P2WPKH signs a 32-byte BIP-143 digest with a 32-byte secp256k1
// private key. The signature is low-S DER with the SIGHASH_ALL byte, and the
// public key is compressed, which is the witness a P2WPKH input carries.
// Encoding matches the previous adapter signer. The caller zeros privateKey.
func SignSecp256k1P2WPKH(privateKey, digest []byte) (signature, compressedPublic []byte, err error) {
	if len(privateKey) != 32 {
		return nil, nil, fmt.Errorf("btc private key must be 32 bytes")
	}
	if len(digest) != 32 {
		return nil, nil, fmt.Errorf("btc sighash must be 32 bytes")
	}
	priv, _ := btcec.PrivKeyFromBytes(privateKey)
	signature = append(ecdsa.Sign(priv, digest).Serialize(), bitcoinSighashAll)
	return signature, priv.PubKey().SerializeCompressed(), nil
}
