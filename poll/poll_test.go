package poll

import (
	"testing"
	"time"
)

func TestAdaptivePoll(t *testing.T) {
	tests := []struct {
		attempt int
		base    time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{0, 10 * time.Millisecond, 200 * time.Millisecond, 10 * time.Millisecond},
		{1, 10 * time.Millisecond, 200 * time.Millisecond, 20 * time.Millisecond},
		{2, 10 * time.Millisecond, 200 * time.Millisecond, 40 * time.Millisecond},
		{3, 10 * time.Millisecond, 200 * time.Millisecond, 80 * time.Millisecond},
		{4, 10 * time.Millisecond, 200 * time.Millisecond, 160 * time.Millisecond},
		{5, 10 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond},
		{6, 10 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond},
		{-1, 10 * time.Millisecond, 200 * time.Millisecond, 10 * time.Millisecond},
		{1000, 10 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond},
		{1, 0, 200 * time.Millisecond, 0},
		{1, 10 * time.Millisecond, 0, 0},
	}
	for _, tc := range tests {
		got := AdaptivePoll(tc.attempt, tc.base, tc.max)
		if got != tc.want {
			t.Errorf("AdaptivePoll(%d, %v, %v) = %v, want %v", tc.attempt, tc.base, tc.max, got, tc.want)
		}
	}
}
