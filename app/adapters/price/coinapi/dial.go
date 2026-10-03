package coinapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/macrowallets/waas/app/services/price"
)

// Dialer opens a CoinAPI quote socket with the default gorilla dialer and no extra headers.
type Dialer struct{}

// Dial opens url. An empty url fails before any network call. Dial errors are returned as-is.
func (Dialer) Dial(ctx context.Context, url string) (price.QuoteConn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("coinapi websocket dial: context is nil")
	}
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("coinapi websocket dial: url is required")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
