package deadline

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWait_Returns_TheResultOfACallWithinItsBudget(t *testing.T) {
	value, err := Wait(time.Second, func() (int, error) { return 7, nil })
	if err != nil || value != 7 {
		t.Fatalf("value %d, err %v", value, err)
	}
	failure := errors.New("refused")
	if _, err := Wait(time.Second, func() (int, error) { return 0, failure }); !errors.Is(err, failure) {
		t.Fatalf("err %v, want the call's own error", err)
	}
}

func TestWait_StopsWaitingForACallThatIgnoresItsDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := time.Now()
	_, err := Wait(50*time.Millisecond, func() (int, error) {
		<-release
		return 1, nil
	})
	if !errors.Is(err, ErrExceeded) {
		t.Fatalf("err %v, want ErrExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waited %s for a 50 ms budget", elapsed)
	}
}

func TestWait_TurnsAPanicIntoAnError(t *testing.T) {
	_, err := Wait(time.Second, func() (int, error) { panic("store client crashed") })
	if !errors.Is(err, ErrPanicked) || !strings.Contains(err.Error(), "store client crashed") {
		t.Fatalf("err %v", err)
	}
}
