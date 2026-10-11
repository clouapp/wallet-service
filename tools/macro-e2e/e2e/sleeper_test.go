package e2e

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fake e2e API is a copy of this test binary started with sleeperEnv: it sleeps
// instead of running the tests. A copy of /bin/sleep cannot stand in on macOS, where
// the kernel kills a copied platform (arm64e) binary at exec.
const (
	sleeperEnv        = "WAAS_E2E_TEST_SLEEPER"
	sleeperPlain      = "sleep"
	sleeperIgnoreTerm = "ignore-term"
	sleeperReady      = "sleeper ready"
	sleeperStartup    = 5 * time.Second
)

func TestMain(m *testing.M) {
	if mode, ok := os.LookupEnv(sleeperEnv); ok {
		os.Exit(runSleeper(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// runSleeper sleeps for the seconds in args[0]. With sleeperIgnoreTerm it ignores
// SIGTERM first; an ignored SIGTERM inherited from a shell does not survive a Go
// exec, the runtime installs its own handler. It says sleeperReady once set up.
func runSleeper(mode string, args []string) int {
	if len(args) != 1 {
		return 2
	}
	seconds, err := strconv.Atoi(args[0])
	if err != nil {
		return 2
	}
	if mode == sleeperIgnoreTerm {
		signal.Ignore(syscall.SIGTERM)
	}
	fmt.Println(sleeperReady)
	time.Sleep(time.Duration(seconds) * time.Second)
	return 0
}

// testBinary is this test binary, the source of the fake API's copies.
func testBinary(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// waitForSleeper reads the sleeper's first line: once it says it is ready, its
// SIGTERM handling is in place.
func waitForSleeper(t *testing.T, stdout io.Reader) {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		text, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- strings.TrimSpace(text)
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case got := <-line:
		if got != sleeperReady {
			t.Fatalf("fake API said %q, want %q", got, sleeperReady)
		}
	case <-time.After(sleeperStartup):
		t.Fatal("fake API never got ready")
	}
}
