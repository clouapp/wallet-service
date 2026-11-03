package middleware

import (
	"fmt"
	"net"
	"strings"

	contractshttp "github.com/goravel/framework/contracts/http"
)

// ClientIP makes ctx.Request().Ip() the address the request really came from.
//
// The gin engine goravel builds trusts every hop and reads X-Forwarded-For
// before anything else, and the driver offers no setting for either: left
// alone, a client sets its own address, and with it the rate-limit bucket it
// is counted in. This middleware runs before any other and leaves gin only
// one header to read: header's value when the connection comes from one of
// trustedProxies (the proxy overwrites it, so it cannot be forged), and none
// otherwise, which makes gin fall back to the connection's own address.
//
// trustedProxies is a comma-separated list of CIDRs or addresses. A malformed
// entry panics: it is boot-time configuration, and a proxy trusted by mistake
// is exactly the hole this closes.
func ClientIP(trustedProxies, header string) contractshttp.Middleware {
	trusted := parseTrustedProxies(trustedProxies)
	return func(ctx contractshttp.Context) {
		req := ctx.Request().Origin()
		vouched := strings.TrimSpace(req.Header.Get(header))
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Real-IP")
		if fromTrustedProxy(req.RemoteAddr, trusted) && net.ParseIP(vouched) != nil {
			req.Header.Set("X-Real-IP", vouched)
		}
		ctx.Request().Next()
	}
}

func parseTrustedProxies(list string) []*net.IPNet {
	var trusted []*net.IPNet
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			ip := net.ParseIP(entry)
			if ip == nil {
				panic(fmt.Sprintf("http.trusted_proxies: %q is not an address or a CIDR", entry))
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			entry = fmt.Sprintf("%s/%d", entry, bits)
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			panic(fmt.Sprintf("http.trusted_proxies: %q is not an address or a CIDR", entry))
		}
		trusted = append(trusted, network)
	}
	return trusted
}

func fromTrustedProxy(remoteAddr string, trusted []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
