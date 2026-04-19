package sweep

import (
	"math/big"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Strategy enumerates the withdrawal-source policy outcomes.
type Strategy string

const (
	StrategyDirectFromBase  Strategy = "direct_from_base"
	StrategyDirectFromChild Strategy = "direct_from_child"
	StrategyMultiSweep      Strategy = "multi_sweep"
	StrategyInsufficient    Strategy = "insufficient"
)

// AddressBalance records a wallet address + its balance for plan transparency.
type AddressBalance struct {
	Address models.Address
	Balance *big.Int
	Reason  string // filled for dust-ignored entries
}

// PlannedSweep is one sweep leg in a multi_sweep plan.
type PlannedSweep struct {
	From      models.Address
	Amount    *big.Int
	NeedsGas  bool     // true → chain requires a gas_seed tx before the sweep (EVM ERC-20)
	GasAmount *big.Int // size of the gas_seed tx (optional; executor may compute its own)
}

// Plan is the output of PlanForWithdrawal / ConsolidateAll; consumed by ExecutePlan.
type Plan struct {
	WalletID      uuid.UUID
	Chain         string
	Asset         string
	Amount        *big.Int
	Strategy      Strategy
	SourceAddress *models.Address // set for direct_from_base / direct_from_child
	Sweeps        []PlannedSweep  // set for multi_sweep
	EstimatedGas  *big.Int
	ReachesTarget bool
	DustIgnored   []AddressBalance
	BaseBalance   *big.Int
}

// CompletedSweep is a sweep leg that successfully broadcast.
type CompletedSweep struct {
	From         models.Address
	TxHash       string
	InternalTxID uuid.UUID
}

// FailedStep records mid-plan failure position for idempotent retry.
type FailedStep struct {
	Index      int
	LastError  string
	RetryReady bool
}

// Result is the output of ExecutePlan / ConsolidateAll.
type Result struct {
	WithdrawalTxID  uuid.UUID
	Sweeps          []CompletedSweep
	FinalWithdrawTx *models.Transaction
	FailedStep      *FailedStep
}

// GasStatus snapshots the gas-readiness check for a wallet.
type GasStatus struct {
	Status        string // "unseeded" | "seeded" | "low"
	BaseAddress   string
	NativeAsset   string
	NativeBalance *big.Int
	Threshold     *big.Int
	LastCheckedAt int64 // unix seconds
}

// Limits is the per-account rate-limit config resolved from accounts.sweep_limits
// (with config.SweepDefaults filled in for missing keys).
type Limits struct {
	MaxAddressesPerRequest  map[string]int // chain-type → max
	MaxConsolidateReqPerDay int
	DailyWithdrawCapUSD     *float64 // nil = unlimited
}
