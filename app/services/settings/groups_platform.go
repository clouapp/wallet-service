package settings

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	groupDepositScan = "deposit_scan"

	sectionScanning = "scanning"

	keyBatchBlocks     = "batch_blocks"
	keyCatchUpBlocks   = "catch_up_blocks"
	keyScanConcurrency = "concurrency"

	// maxDepositScanConcurrency matches deposit.MaxScanConcurrency. A stored
	// value above it is invalid and the scanner keeps the environment window.
	maxDepositScanConcurrency = 32
)

func platformGroups() []Group {
	return []Group{
		{
			Name:    groupDepositScan,
			Scope:   ScopePlatform,
			Section: sectionScanning,
			Block:   "Deposits",
			Settings: []Definition{
				{
					Key:     keyBatchBlocks,
					Label:   "Batch blocks",
					Help:    "Blocks read in one cycle when the scanner is near the chain head. 0 keeps the environment default.",
					Type:    TypeInt,
					Default: func() any { return 0 },
				},
				{
					Key:     keyCatchUpBlocks,
					Label:   "Catch-up blocks",
					Help:    "Blocks read in one cycle while the scanner is behind the chain head. 0 keeps the environment default.",
					Type:    TypeInt,
					Default: func() any { return 0 },
				},
				{
					Key:     keyScanConcurrency,
					Label:   "Scan concurrency",
					Help:    "How many blocks are fetched at once. 0 keeps the environment default.",
					Type:    TypeInt,
					Default: func() any { return 0 },
				},
			},
			Validate: validateDepositScan,
		},
	}
}

func validateDepositScan(effective map[string]string) error {
	fields := map[string][]string{}
	batch, batchOK := nonNegativeSetting(effective[keyBatchBlocks])
	catchUp, catchUpOK := nonNegativeSetting(effective[keyCatchUpBlocks])
	concurrency, concurrencyOK := nonNegativeSetting(effective[keyScanConcurrency])
	if !batchOK {
		fields[keyBatchBlocks] = []string{"must be greater than or equal to 0"}
	}
	if !catchUpOK {
		fields[keyCatchUpBlocks] = []string{"must be greater than or equal to 0"}
	}
	if !concurrencyOK || concurrency > maxDepositScanConcurrency {
		fields[keyScanConcurrency] = []string{fmt.Sprintf("must be between 0 and %d", maxDepositScanConcurrency)}
	}
	if batchOK && catchUpOK && batch > 0 && catchUp > 0 && catchUp < batch {
		fields[keyCatchUpBlocks] = []string{"must not be smaller than batch_blocks"}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

func nonNegativeSetting(raw string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}
