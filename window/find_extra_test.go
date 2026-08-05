package window

import (
	"testing"
	"time"
)

// ── clampPoll ─────────────────────────────────────────────────────────────────

func TestClampPoll_Zero(t *testing.T) {
	got := clampPoll(0)
	if got != 10*time.Millisecond {
		t.Fatalf("clampPoll(0) = %v, want 10ms", got)
	}
}

func TestClampPoll_Negative(t *testing.T) {
	got := clampPoll(-5 * time.Millisecond)
	if got != 10*time.Millisecond {
		t.Fatalf("clampPoll(-5ms) = %v, want 10ms", got)
	}
}

func TestClampPoll_Positive(t *testing.T) {
	got := clampPoll(20 * time.Millisecond)
	if got != 20*time.Millisecond {
		t.Fatalf("clampPoll(20ms) = %v, want 20ms", got)
	}
}
