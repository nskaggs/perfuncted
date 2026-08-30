// Package transport classifies transient transport-level failures.
package transport

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
)

// Classification identifies the transport failure family.
type Classification int

const (
	// ClassUnknown is used when an error is nil or not recognized as transport-level.
	ClassUnknown Classification = iota
	// ClassTimeout covers deadline and I/O timeout failures.
	ClassTimeout
	// ClassConnectionReset covers reset and broken-pipe failures.
	ClassConnectionReset
	// ClassConnectionClosed covers closed network connection failures.
	ClassConnectionClosed
)

// IsRetryable reports whether err is a transient transport failure that may
// succeed on a later attempt.
func IsRetryable(err error) bool {
	return Classify(err) != ClassUnknown
}

// Classify returns the transport failure family for err. Errno identity is
// checked first so classification survives localized or cgo-sourced error
// text; the substring pass remains as a fallback for wrapped errors whose
// cause is not an errno (for example strings embedded from external tool
// output).
func Classify(err error) Classification {
	if err == nil {
		return ClassUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ETIMEDOUT) {
		return ClassTimeout
	}
	if errors.Is(err, net.ErrClosed) {
		return ClassConnectionClosed
	}

	switch {
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return ClassConnectionReset
	case errors.Is(err, syscall.ECONNABORTED):
		return ClassConnectionReset
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ClassTimeout
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "broken pipe"):
		return ClassConnectionReset
	case strings.Contains(msg, "connection reset by peer"):
		return ClassConnectionReset
	case strings.Contains(msg, "closed network connection"):
		return ClassConnectionClosed
	case strings.Contains(msg, "i/o timeout"):
		return ClassTimeout
	case strings.Contains(msg, "context deadline exceeded"):
		return ClassTimeout
	default:
		return ClassUnknown
	}
}
