package testutil

import (
	"net"
	"testing"
)

// ClosedLocalURL is http://127.0.0.1:<port> on a port that was just free, so a
// request to it is refused at once. Port 1 is not: on WSL2 a connection to
// 127.0.0.1:1 hangs until the client timeout instead of being refused.
func ClosedLocalURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a local port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the local port: %v", err)
	}
	return "http://" + addr
}
