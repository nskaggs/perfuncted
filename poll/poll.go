package poll

import "time"

// AdaptivePoll returns an exponentially backing-off poll duration. base is
// the starting duration and max is the cap.
func AdaptivePoll(attempt int, base, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base * time.Duration(1<<attempt)
	if d > max {
		return max
	}
	return d
}
