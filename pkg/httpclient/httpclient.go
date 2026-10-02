// Package httpclient builds the HTTP clients used for long-lived connections to chain
// providers (RPC nodes, Esplora, block explorers).
package httpclient

import (
	"fmt"
	"net/http"
	"time"
)

const (
	// A provider connection that sends no frame for this long gets an HTTP/2 PING.
	pingAfterIdleRead = 15 * time.Second
	// A PING left unanswered this long closes the connection.
	pingTimeout = 10 * time.Second
)

var sharedTransport = newTransport(pingAfterIdleRead, pingTimeout)

// New returns a client with the given total request timeout over a transport whose
// HTTP/2 connections are health-checked. Without the check, a connection that dies
// silently (network change, NAT drop, Wi-Fi switch) keeps receiving every request until
// the kernel gives up retransmitting, which takes 15 to 30 minutes; each request in
// between only ends at the client timeout.
func New(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		panic(fmt.Sprintf("httpclient: timeout must be positive, got %s", timeout))
	}
	return &http.Client{Timeout: timeout, Transport: sharedTransport}
}

func newTransport(pingAfter, pingWait time.Duration) *http.Transport {
	transport := baseTransport()
	transport.HTTP2 = &http.HTTP2Config{
		SendPingTimeout: pingAfter,
		PingTimeout:     pingWait,
	}
	return transport
}

func baseTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
}
