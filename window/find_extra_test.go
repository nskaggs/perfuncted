package window

import (
	"context"
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

// ── WaitForClose ──────────────────────────────────────────────────────────────

func TestWaitForClose_SucceedsWhenAbsent(t *testing.T) {
	// Window is not present — WaitForClose should return immediately.
	m := &fakeManager{wins: []Info{{ID: 1, Title: "OtherWindow"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := WaitForClose(ctx, m, "neverpresent", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForClose returned unexpected error: %v", err)
	}
}

func TestWaitForClose_Timeout(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "StillOpen"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := WaitForClose(ctx, m, "stillopen", 10*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForClose expected timeout error, got nil")
	}
}
