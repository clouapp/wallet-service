package models

// DepositFailureReorgedOut is the failure code (transactions.error_message, and
// failure_code of deposit.failed) of a deposit whose block was reorganized away
// and whose transaction no block of the chain holds any more: nothing reached the
// address. A deposit whose transaction failed on chain carries
// WithdrawalFailureOnChainPrefix + its result instead (onchain_out_of_gas,
// onchain_reverted).
const DepositFailureReorgedOut = "reorged_out"
