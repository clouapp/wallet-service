package coinapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// Dialer opens a CoinAPI quote socket with the gorilla dialer, no extra headers,
// and every TCP connection opened by its dial: the RPC guard's DialContext, so the
// socket never reaches a link-local or cloud metadata address.
type Dialer struct {
	dial httpclient.DialFunc
}

// NewDialer builds the quote socket opener over dial; dial is required.
func NewDialer(dial httpclient.DialFunc) Dialer {
	if dial == nil {
		panic("coinapi websocket: dial is required")
	}
	return Dialer{dial: dial}
}

// Dial opens url. An empty url, or a Dialer without its dial, fails before any
// network call. A dial error, the guard's refusal included, drops the URL before it
// is returned.
func (d Dialer) Dial(ctx context.Context, rawURL string) (price.QuoteConn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("coinapi websocket dial: context is nil")
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("coinapi websocket dial: url is required")
	}
	if d.dial == nil {
		return nil, fmt.Errorf("coinapi websocket dial: no connection guard")
	}
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = d.dial
	conn, response, err := dialer.DialContext(ctx, rawURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, httpclient.RedactURL(err, rawURL)
	}
	return conn, nil
}
