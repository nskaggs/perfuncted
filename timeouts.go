package perfuncted

import "time"

// TimeoutPolicy is the small shared timing policy for managed desktop
// startup and operations. Callers can override the policy as one unit through
// SessionConfig; derived waits must use these values instead of introducing a
// second host-specific timeout.
type TimeoutPolicy struct {
	Short      time.Duration
	Medium     time.Duration
	Long       time.Duration
	Startup    time.Duration
	Diagnostic time.Duration
	Poll       time.Duration
}

// DefaultTimeoutPolicy is intentionally generous for slow desktop hosts.
// Parent contexts still cap every operation, so a fast caller is not forced
// to wait for the full policy.
var DefaultTimeoutPolicy = TimeoutPolicy{
	Short:      30 * time.Second,
	Medium:     2 * time.Minute,
	Long:       5 * time.Minute,
	Startup:    20 * time.Minute,
	Diagnostic: 15 * time.Second,
	Poll:       250 * time.Millisecond,
}

// WithDefaults fills an incomplete policy from DefaultTimeoutPolicy.
func (p TimeoutPolicy) WithDefaults() TimeoutPolicy {
	d := DefaultTimeoutPolicy
	if p.Short <= 0 {
		p.Short = d.Short
	}
	if p.Medium <= 0 {
		p.Medium = d.Medium
	}
	if p.Long <= 0 {
		p.Long = d.Long
	}
	if p.Startup <= 0 {
		p.Startup = d.Startup
	}
	if p.Diagnostic <= 0 {
		p.Diagnostic = d.Diagnostic
	}
	if p.Poll <= 0 {
		p.Poll = d.Poll
	}
	return p
}
