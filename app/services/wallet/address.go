package wallet

import "github.com/macrowallets/waas/app/services/addressing"

// Thin aliases over the addressing package so internal call sites keep the
// short names they've always used. The actual logic lives in addressing/.

func deriveEthAddress(compressedPubKey []byte) (string, error) {
	return addressing.DeriveEthAddress(compressedPubKey)
}

func deriveBtcAddress(hrp string, compressedPubKey []byte) (string, error) {
	return addressing.DeriveBtcAddress(hrp, compressedPubKey)
}

func deriveSolAddress(pubKey []byte) (string, error) {
	return addressing.DeriveSolAddress(pubKey)
}

// testnetReporter is a chain adapter that knows whether its record is on a test
// network (Bitcoin: it selects tb1 over bc1).
type testnetReporter interface {
	IsTestnet() bool
}

// deriveChainAddress derives the address on the network the registered adapter of
// chainID points at, so a "btc" record configured for testnet yields tb1.
func (s *Service) deriveChainAddress(chainID string, pubKey []byte) (string, error) {
	return addressing.DeriveAddressOnNetwork(chainID, s.chainIsTestnet(chainID), pubKey)
}

func (s *Service) chainIsTestnet(chainID string) bool {
	if s.registry == nil {
		return false
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return false
	}
	reporter, ok := adapter.(testnetReporter)
	return ok && reporter.IsTestnet()
}
