package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
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
	errContextDeadlineStr     = errors.New("operation failed: context deadline exceeded")
	errUnexpectedScriptResult = errors.New("unexpected script result")
)

type customTimeoutError struct{}

func (customTimeoutError) Error() string   { return "custom network timeout" }
func (customTimeoutError) Timeout() bool   { return true }
func (customTimeoutError) Temporary() bool { return true }

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
		{name: "net.ErrClosed", err: fmt.Errorf("wayland: %w", net.ErrClosed), want: ClassConnectionClosed},
		{name: "unrelated error", err: errHTTPNotFound, want: ClassUnknown},
		// Errno identity must win even when the message text is not the Go
		// default (localized or cgo-sourced strings).
		{name: "errno ECONNRESET", err: fmt.Errorf("sway ipc: %w", syscall.ECONNRESET), want: ClassConnectionReset},
		{name: "errno EPIPE", err: fmt.Errorf("write: %w", syscall.EPIPE), want: ClassConnectionReset},
		{name: "errno ECONNABORTED", err: fmt.Errorf("read: %w", syscall.ECONNABORTED), want: ClassConnectionReset},
		{name: "errno ETIMEDOUT", err: fmt.Errorf("connect: %w", syscall.ETIMEDOUT), want: ClassTimeout},
		{name: "custom net.Error timeout", err: customTimeoutError{}, want: ClassTimeout},
		{name: "localized message with errno cause", err: fmt.Errorf("verbindung vom gegenstelle zurückgesetzt: %w", syscall.ECONNRESET), want: ClassConnectionReset},
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
