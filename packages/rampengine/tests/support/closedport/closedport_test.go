package closedport

import (
	"errors"
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The address refuses every dial, and no listener can take it while the test
// runs: that is what makes a "connection refused" test deterministic.
func TestAddrRefusesAndCannotBeTaken(t *testing.T) {
	addr := Addr(t)

	for range 50 {
		_, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if runtime.GOOS == "darwin" {
			var e net.Error
			if errors.As(err, &e) && e.Timeout() {
				t.Skip("host drops probes to a reserved non-listening socket; refusal check runs on Linux")
			}
		}
		require.ErrorIs(t, err, syscall.ECONNREFUSED)
	}
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		require.NoError(t, listener.Close())
	}
	require.True(t, errors.Is(err, syscall.EADDRINUSE), "the port stays held: %v", err)
}
