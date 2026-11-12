package main

import (
	"testing"
	"time"
)

// The screen-settle poller reuses one timer across captures. A previously
// expired timer must not wake the next polling interval immediately, and a
// stopped timer must remain reusable without a stale notification.
func TestScreenPollTimerStopDrainAllowsSafeReuse(t *testing.T) {
	const poll = 15 * time.Millisecond
	for _, scenario := range []string{"active", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			timer := time.NewTimer(time.Hour)
			defer timer.Stop()
			if scenario == "expired" {
				if !timer.Stop() {
					t.Fatal("fresh long timer already expired")
				}
				timer.Reset(time.Nanosecond)
				select {
				case <-timer.C:
				case <-time.After(time.Second):
					t.Fatal("expired timer notification missing")
				}
			}
			stopAndDrainTimer(timer)
			for attempt := 0; attempt < 4; attempt++ {
				started := time.Now()
				timer.Reset(poll)
				select {
				case <-timer.C:
					if elapsed := time.Since(started); elapsed < poll/2 {
						t.Fatalf("stale timer wakeup after %s (expected interval %s)", elapsed, poll)
					}
				case <-time.After(time.Second):
					t.Fatal("reused screen polling timer never fired")
				}
				stopAndDrainTimer(timer)
			}
		})
	}
}
