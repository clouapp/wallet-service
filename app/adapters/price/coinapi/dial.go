package coinapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// Dialer opens a CoinAPI quote socket with the default gorilla dialer and no extra headers.
type Dialer struct{}

// Dial opens url. An empty url fails before any network call. A dial error
// drops the URL before it is returned.
func (Dialer) Dial(ctx context.Context, rawURL string) (price.QuoteConn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("coinapi websocket dial: context is nil")
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("coinapi websocket dial: url is required")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		return nil, httpclient.RedactURL(err, rawURL)
	}
	return conn, nil
}
