package perfuncted

import (
	"math"
	"testing"
	"time"
)

// Huge but valid positive startup policies must not wrap the ceiling
// division into a single readiness attempt. That would shorten an explicit
// startup allowance to one poll rather than preserving the caller's policy.
func TestStartupWaitAttemptsDoesNotOverflowCeilingDivision(t *testing.T) {
	for _, tc := range []struct {
		startup time.Duration
		poll    time.Duration
		want    int
	}{
		{time.Duration(math.MaxInt64), 2 * time.Nanosecond, int(math.MaxInt64/2 + 1)},
		{time.Duration(math.MaxInt64), 3 * time.Nanosecond, int(math.MaxInt64/3 + 1)},
		{time.Duration(math.MaxInt64), time.Nanosecond, int(math.MaxInt64)},
	} {
		got := startupWaitAttempts(TimeoutPolicy{Startup: tc.startup, Poll: tc.poll})
		if got != tc.want {
			t.Errorf("startupWaitAttempts(startup=%s, poll=%s)=%d; want %d", tc.startup, tc.poll, got, tc.want)
		}
	}
}
