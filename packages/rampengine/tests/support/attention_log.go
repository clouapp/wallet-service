package support

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	mockslog "github.com/goravel/framework/mocks/log"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// AttentionMarker opens every log line an operator is meant to be alerted by.
// The alert rules match on it, so the text after it may change and the marker
// may not.
const AttentionMarker = "ramp.attention:"

// ErrorLines records every formatted Errorf line a mocked logger receives.
type ErrorLines struct {
	mu    sync.Mutex
	lines []string
}

// CaptureErrorLines makes logger accept Errorf with up to five arguments and
// record the formatted line. The caller still wires the logger into the
// facade (and With/WithContext when the code under test chains them).
func CaptureErrorLines(logger *mockslog.Log) *ErrorLines {
	captured := &ErrorLines{}
	record := func(args testifymock.Arguments) {
		format, _ := args.Get(0).(string)
		captured.mu.Lock()
		defer captured.mu.Unlock()
		captured.lines = append(captured.lines, fmt.Sprintf(format, args[1:]...))
	}
	for extra := 0; extra <= 4; extra++ {
		matchers := make([]any, 1+extra)
		for i := range matchers {
			matchers[i] = testifymock.Anything
		}
		logger.On("Errorf", matchers...).Run(record).Return().Maybe()
	}
	return captured
}

// Lines is a copy of what was recorded.
func (e *ErrorLines) Lines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.lines...)
}

// RequireAttention fails unless a line starts with the marker, and unless no
// line (marked or not) contains any of the forbidden values: an amount or an
// address must never reach the log. It reads the prefix only, never the text
// after it.
func (e *ErrorLines) RequireAttention(t *testing.T, forbidden ...string) {
	t.Helper()
	lines := e.Lines()
	marked := false
	for _, line := range lines {
		if strings.HasPrefix(line, AttentionMarker) {
			marked = true
		}
		for _, value := range forbidden {
			require.NotContains(t, line, value, "an attention line carries no amount or address")
		}
	}
	require.True(t, marked, "no log line starts with %q; got %q", AttentionMarker, lines)
}
