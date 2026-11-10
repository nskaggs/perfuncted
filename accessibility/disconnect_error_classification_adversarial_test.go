package accessibility

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDisconnectClassificationPreservesWrappedErrorSemantics(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"identity", ErrDisconnected, true},
		{"wrapped identity", fmt.Errorf("cannot fetch children: %w", ErrDisconnected), true},
		{"lowercase transport", errors.New("connection disconnected by peer"), true},
		{"uppercase transport", errors.New("DBUS DISCONNECTED"), true},
		{"recipient unavailable", errors.New("Message Recipient has disconnected"), true},
		{"no reply", errors.New("No Reply from service"), true},
		{"unrelated permission", errors.New("permission denied"), false},
		{"unrelated timeout", errors.New("context deadline exceeded"), false},
		{"unrelated internal", errors.New("method unavailable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDisconnectedDBusError(tc.err); got != tc.want {
				t.Fatalf("isDisconnectedDBusError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMatchErrorFormattingUnwrapAndNilCases(t *testing.T) {
	cause := errors.New("secret cause")
	cases := []struct {
		name      string
		value     *MatchError
		want      string
		wantCause error
	}{
		{"nil pointer", nil, "accessibility: match failed", nil},
		{"empty", &MatchError{}, "accessibility: match failed", nil},
		{"operation only", &MatchError{Operation: "find"}, "accessibility: find: match failed", nil},
		{"cause only", &MatchError{Err: cause}, "secret cause", cause},
		{"both", &MatchError{Operation: "find", Err: cause}, "accessibility: find: secret cause", cause},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.value.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
			if got := tc.value.Unwrap(); got != tc.wantCause { //nolint:errorlint // Unwrap must preserve the exact cause object.
				t.Fatalf("Unwrap() = %v, want %v", got, tc.wantCause)
			}
			if tc.wantCause != nil && !errors.Is(tc.value, tc.wantCause) {
				t.Fatal("wrapped cause no longer discoverable")
			}
			if strings.Contains(tc.value.Error(), "%!") {
				t.Fatal("malformed diagnostic")
			}
		})
	}
}
