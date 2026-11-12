//go:build linux

package gnomebridge

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Older GNOME extensions may return an untyped D-Bus error whose message
// identifies a missing window. Preserve the original error while translating
// only that specific failure into the public object-not-found sentinel.
func TestGNOMEUnknownDBusErrorWindowIdentityFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		message      string
		wantNotFound bool
	}{
		{"missing window lowercase", "window not found", true},
		{"missing window mixed case", "Window Not Found: 42", true},
		{"unrelated permission rejection", "permission denied", false},
		{"similar but distinct error", "window unavailable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := dbus.NewError("org.example.LegacyExtension.Error", []any{tc.message})
			for _, err := range []error{original, *original, fmt.Errorf("wrapped error: %w", *original)} {
				translated := translateDBusError(err)
				if errors.Is(translated, ErrObjectNotFound) != tc.wantNotFound {
					t.Fatalf("incorrect not-found classification for %T %q: %v", err, tc.message, translated)
				}
				if !strings.Contains(translated.Error(), tc.message) {
					t.Fatalf("lost original D-Bus error for %T %q: %v", err, tc.message, translated)
				}
				if errors.Is(translated, ErrUnavailable) {
					t.Fatalf("legacy extension error unexpectedly classified as unavailable: %v", translated)
				}
			}
		})
	}
}
