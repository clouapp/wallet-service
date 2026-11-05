// Package closedport gives a test a loopback address that refuses every
// connection. It is its own package, importing nothing of the app, so an
// adapter's internal test can use it without the import cycle tests/support
// would bring.
package closedport

import (
	"fmt"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Addr is a 127.0.0.1 address that refuses every connection for the life of
// the test: a TCP socket is bound to it and never listens, so a dial gets
// ECONNREFUSED and no other socket can bind the port meanwhile.
//
// Binding a listener, closing it and dialing its port instead races every
// process that binds the freed port in between. On a loaded machine another
// suite's server does, the dial connects, and a test of "the request was never
// sent" sees a connection reset instead.
func Addr(t testing.TB) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, syscall.Close(fd)) })
	require.NoError(t, syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}))
	bound, err := syscall.Getsockname(fd)
	require.NoError(t, err)
	inet, ok := bound.(*syscall.SockaddrInet4)
	require.True(t, ok, "a socket bound to 127.0.0.1 has an IPv4 name")
	return fmt.Sprintf("127.0.0.1:%d", inet.Port)
}
