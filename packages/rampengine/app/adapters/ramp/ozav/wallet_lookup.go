package ozav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type walletDetailResponse struct {
	ID         string `json:"id"`
	CustomerID string `json:"customer_id"`
}

// CustomerIDForWallet resolves the Ozav customer that owns a managed wallet.
func (a *Adapter) CustomerIDForWallet(ctx context.Context, walletID string) (string, error) {
	if walletID == "" {
		return "", errors.New("ozav: wallet id is required")
	}
	var resp walletDetailResponse
	path := fmt.Sprintf("/wallets/%s", walletID)
	if err := a.do(ctx, Request{Method: http.MethodGet, Path: path}, &resp); err != nil {
		return "", err
	}
	if resp.CustomerID == "" {
		return "", fmt.Errorf("ozav: wallet %s has no customer_id", walletID)
	}
	return resp.CustomerID, nil
}
