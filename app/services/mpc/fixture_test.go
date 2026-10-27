package mpc

import "github.com/macrowallets/waas/tests/tssfixture"

// newTestTSSService is the TSS service of the tests: its secp256k1 keygens take the
// stored pre-parameters (tests/tssfixture) instead of searching for safe primes.
func newTestTSSService() *TSSService {
	return NewTSSServiceWithPreParamsFrom(tssfixture.Generator())
}
