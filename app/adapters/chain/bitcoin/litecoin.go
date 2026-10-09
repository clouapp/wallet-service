package bitcoin

import (
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
)

// The Litecoin parameters are never registered with chaincfg.Register: registration
// is process-global and would make btcutil decode ltc1 strings for every caller.
// The adapter decodes its own bech32 addresses (witnessProgram) instead. The
// prefixes live in app/services/addressing, which also serves the key export.
var (
	ltcMainNetParams = *addressing.BitcoinFamilyParams(models.ChainLTC, false)
	ltcTestNetParams = *addressing.BitcoinFamilyParams(models.ChainLTC, true)
)

// Litecoin Core policy (src/policy/policy.h at the same commit): DUST_RELAY_TX_FEE is
// 30,000 sat/kvB, ten times Bitcoin's 3,000, while DEFAULT_MIN_RELAY_TX_FEE
// (src/validation.h) is 1,000 sat/kvB = 1 sat/vB, as in Bitcoin. GetDustThreshold
// (src/policy/policy.cpp) charges an output its own size plus the input that spends
// it: 98 vB for P2WPKH (2,940 sats) and 182 B for P2PKH (5,460 sats). Like Bitcoin's
// 546, the limit used is the P2PKH one, which every standard output clears.
const ltcDustSats int64 = 5_460
