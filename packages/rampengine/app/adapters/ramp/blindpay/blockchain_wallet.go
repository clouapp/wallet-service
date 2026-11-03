package blindpay

import (
	"context"
	"fmt"
	"math/big"
	"net/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// solanaAddressLen is the byte length of a Solana public key.
const solanaAddressLen = 32

// blockchainWalletRequest is the create body; the field order is fixed so the
// same input always yields the same bytes.
type blockchainWalletRequest struct {
	Name                 string `json:"name"`
	Network              string `json:"network"`
	Address              string `json:"address"`
	IsAccountAbstraction bool   `json:"is_account_abstraction"`
	IsSelfCustody        bool   `json:"is_self_custody"`
}

type blockchainWalletResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Network    string `json:"network"`
	Address    string `json:"address"`
	CustomerID string `json:"customer_id"`
}

// validSolanaAddress reports whether address is base58 that decodes to 32 bytes.
func validSolanaAddress(address string) bool {
	if address == "" || len(address) > 44 {
		return false
	}
	value := new(big.Int)
	base := big.NewInt(58)
	leadingZeros := 0
	for i := 0; i < len(address); i++ {
		digit := -1
		for j := 0; j < len(base58Alphabet); j++ {
			if base58Alphabet[j] == address[i] {
				digit = j
				break
			}
		}
		if digit < 0 {
			return false
		}
		if value.Sign() == 0 && digit == 0 {
			leadingZeros++
		}
		value.Mul(value, base).Add(value, big.NewInt(int64(digit)))
	}
	return leadingZeros+len(value.Bytes()) == solanaAddressLen
}

// ensureBlockchainWallet answers the bw_ id of address on network for
// customerID, registering it when absent. The wallet is the customer's own
// (self custody) and an account-abstraction one, which is how BlindPay treats a
// Solana wallet it does not custody. The name carries no personal data.
func (a *Adapter) ensureBlockchainWallet(ctx context.Context, client *Client, customerID, network, address string) (string, error) {
	if customerID == "" {
		return "", ramp.ErrIntegrationRequired
	}
	if !validSolanaAddress(address) {
		return "", fmt.Errorf("blindpay: wallet address: %w", ramp.ErrInvalidWallet)
	}
	cacheKey := walletCacheKey(client.InstanceID(), customerID, network, address)
	if id, ok := a.wallets.Load(cacheKey); ok {
		return id.(string), nil
	}
	return shareFlight(ctx, &a.walletFlight, cacheKey, func(flight context.Context) (string, error) {
		return a.findOrCreateBlockchainWallet(flight, client, cacheKey, customerID, network, address)
	})
}

// findOrCreateBlockchainWallet is ensureBlockchainWallet's provider half, run
// once per key at a time (walletFlight).
func (a *Adapter) findOrCreateBlockchainWallet(
	ctx context.Context, client *Client, cacheKey, customerID, network, address string,
) (string, error) {
	// A flight that just finished may have stored it.
	if id, ok := a.wallets.Load(cacheKey); ok {
		return id.(string), nil
	}
	path := "customers/" + customerID + "/blockchain-wallets"
	var list []blockchainWalletResponse
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: path}, &list); err != nil {
		return "", err
	}
	for _, item := range list {
		if item.ID != "" && item.Network == network && item.Address == address {
			a.wallets.Store(cacheKey, item.ID)
			return item.ID, nil
		}
	}
	var created blockchainWalletResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: path,
		IdempotencyKey: a.createKey("bw:" + customerID + ":" + network + ":" + address),
		Body: blockchainWalletRequest{
			Name: "xip-" + address[:8], Network: network, Address: address,
			IsAccountAbstraction: true, IsSelfCustody: true,
		},
	}, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("blindpay: blockchain wallet create answered no id: %w", ramp.ErrProviderFailed)
	}
	a.wallets.Store(cacheKey, created.ID)
	return created.ID, nil
}

func walletCacheKey(instanceID, customerID, network, address string) string {
	return instanceID + "/" + customerID + "/" + network + "/" + address
}

// forgetBlockchainWallet drops a cached wallet id the provider no longer knows.
func (a *Adapter) forgetBlockchainWallet(instanceID, customerID, network, address string) {
	a.wallets.Delete(walletCacheKey(instanceID, customerID, network, address))
}
