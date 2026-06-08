package transport

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

var (
	errIOTimeout              = errors.New("read: i/o timeout")
	errBrokenPipe             = errors.New("write: broken pipe")
	errConnectionReset        = errors.New("read: connection reset by peer")
	errClosedNetwork          = errors.New("use of closed network connection")
	errHTTPNotFound           = errors.New("404 not found")
	errBareConnectionReset    = errors.New("connection reset by peer")
	errBareClosedNetwork      = errors.New("closed network connection")
	errContextDeadlineStr    = errors.New("operation failed: context deadline exceeded")
	errUnexpectedScriptResult = errors.New("unexpected script result")
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Classification
	}{
		{name: "nil error", err: nil, want: ClassUnknown},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: ClassTimeout},
		{name: "wrapped deadline exceeded", err: fmt.Errorf("read: %w", context.DeadlineExceeded), want: ClassTimeout},
		{name: "io timeout", err: errIOTimeout, want: ClassTimeout},
		{name: "context deadline string", err: errContextDeadlineStr, want: ClassTimeout},
		{name: "broken pipe", err: errBrokenPipe, want: ClassConnectionReset},
		{name: "connection reset", err: errConnectionReset, want: ClassConnectionReset},
		{name: "closed connection", err: errClosedNetwork, want: ClassConnectionClosed},
		{name: "unrelated error", err: errHTTPNotFound, want: ClassUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Fatalf("Classify(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "connection reset", err: errBareConnectionReset, want: true},
		{name: "closed connection", err: errBareClosedNetwork, want: true},
		{name: "unrelated error", err: errUnexpectedScriptResult, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.want {
				t.Fatalf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
