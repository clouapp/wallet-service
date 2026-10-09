package walletview

import (
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const evmContractPrefix = "0x"

// contractKey matches EVM contracts case-insensitively and Solana mints exactly.
func contractKey(contract string) string {
	trimmed := strings.TrimSpace(contract)
	if strings.HasPrefix(strings.ToLower(trimmed), evmContractPrefix) {
		return strings.ToLower(trimmed)
	}
	return trimmed
}

// configuredAssetBalances keeps the native balance and the token balances whose
// contract the chain still configures, dropping rows left by removed tokens.
func configuredAssetBalances(rows []models.WalletAssetBalance, activeTokens []models.Token) []models.WalletAssetBalance {
	configured := make(map[string]struct{}, len(activeTokens))
	for _, token := range activeTokens {
		configured[contractKey(token.ContractAddress)] = struct{}{}
	}

	kept := make([]models.WalletAssetBalance, 0, len(rows))
	for _, row := range rows {
		if row.AssetType == string(types.AssetTypeNative) {
			kept = append(kept, row)
			continue
		}
		if row.AssetContract == nil {
			continue
		}
		if _, ok := configured[contractKey(*row.AssetContract)]; ok {
			kept = append(kept, row)
		}
	}
	return kept
}
