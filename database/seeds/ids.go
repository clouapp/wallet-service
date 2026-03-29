package seeds

import "github.com/google/uuid"

// Predefined UUIDs so the seed is idempotent (same IDs every run).
var (
	adminUserID       = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	aliceUserID       = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	bobUserID         = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	acmeAccountID     = uuid.MustParse("00000000-0000-0000-0000-000000000010")
	acmeTestAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000011")
	ethWalletID       = uuid.MustParse("00000000-0000-0000-0000-000000000020")
	btcWalletID       = uuid.MustParse("00000000-0000-0000-0000-000000000021")
	polyWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000022")
	tethWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000023")
	tbtcWalletID      = uuid.MustParse("00000000-0000-0000-0000-000000000024")
	tpolyWalletID     = uuid.MustParse("00000000-0000-0000-0000-000000000025")
)
