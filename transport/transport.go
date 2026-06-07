// Package transport classifies transient transport-level failures.
package transport

import (
	"context"
	"errors"
	"strings"
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

// Classify returns the transport failure family for err.
func Classify(err error) Classification {
	if err == nil {
		return ClassUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) {
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
	default:
		return ClassUnknown
	}
}
