package wallet

import "github.com/macrowallets/waas/app/services/addressing"

// Thin aliases over the addressing package so internal call sites keep the
// short names they've always used. The actual logic lives in addressing/.

func deriveAddress(chainID string, compressedPubKey []byte) (string, error) {
	return addressing.DeriveAddress(chainID, compressedPubKey)
}

func deriveEthAddress(compressedPubKey []byte) (string, error) {
	return addressing.DeriveEthAddress(compressedPubKey)
}

func deriveBtcAddress(hrp string, compressedPubKey []byte) (string, error) {
	return addressing.DeriveBtcAddress(hrp, compressedPubKey)
}

func deriveSolAddress(pubKey []byte) (string, error) {
	return addressing.DeriveSolAddress(pubKey)
}
