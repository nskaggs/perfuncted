package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// An already canceled command must not reveal key validation results or
// attempt any EIS input, regardless of whether the key name is valid.
func TestRemoteDesktopDirectKeyRejectsCanceledContextBeforeKeyValidation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		invoke func(context.Context, string) error
	}{
		{"down", b.KeyDown},
		{"up", b.KeyUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"not-a-real-key", "a"} {
				if err := tc.invoke(canceled, key); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled %s(%q) gave %v", tc.name, key, err)
				}
			}
			if err := tc.invoke(nil, "not-a-real-key"); err == nil || !strings.Contains(err.Error(), "nil operation context") {
				t.Fatalf("nil context %s revealed input validation: %v", tc.name, err)
			}
		})
	}
}
