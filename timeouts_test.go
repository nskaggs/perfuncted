package perfuncted

import (
	"testing"
	"time"
)

func TestDefaultTimeoutPolicyIsShared(t *testing.T) {
	p := DefaultTimeoutPolicy
	if p.Short != 5*time.Second || p.Medium != 30*time.Second ||
		p.Long != 90*time.Second || p.Startup != 10*time.Minute ||
		p.Diagnostic != 5*time.Second || p.Poll != 150*time.Millisecond {
		t.Fatalf("default timeout policy = %#v", p)
	}
}

func TestTimeoutPolicyWithDefaultsFillsOnlyMissingValues(t *testing.T) {
	got := (TimeoutPolicy{Startup: time.Minute}).WithDefaults()
	if got.Startup != time.Minute {
		t.Fatalf("explicit startup timeout = %s, want 1m", got.Startup)
	}
	if got.Short != DefaultTimeoutPolicy.Short || got.Poll != DefaultTimeoutPolicy.Poll {
		t.Fatalf("missing policy values = %#v, want defaults", got)
	}
}

func TestStartupWaitAttemptsUsesConfiguredPolicy(t *testing.T) {
	got := startupWaitAttempts(TimeoutPolicy{
		Startup: 4 * time.Second,
		Poll:    time.Second,
	})
	if got != 4 {
		t.Fatalf("startup wait attempts = %d, want 4", got)
	}
}
