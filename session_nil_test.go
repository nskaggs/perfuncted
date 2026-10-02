package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
)

// Every exported Session member must tolerate a nil receiver. Three accessors
// dereferenced the session directly and panicked, which turned a documented
// defensive case into a crash for library callers.
func TestNilSessionAccessorsDoNotPanic(t *testing.T) {
	var s *Session

	stringAccessors := map[string]func() string{
		"XDG":            s.XDG,
		"DBusAddress":    s.DBusAddress,
		"WaylandDisplay": s.WaylandDisplay,
		"X11Display":     s.X11Display,
		"LogPath":        s.LogPath,
		"SwaySocket":     s.SwaySocket,
	}
	for name, accessor := range stringAccessors {
		t.Run(name, func(t *testing.T) {
			// A panic fails the test; the value must simply be empty.
			if got := accessor(); got != "" {
				t.Fatalf("%s() = %q, want the empty string", name, got)
			}
		})
	}

	t.Run("Env", func(t *testing.T) {
		if got := s.Env(); len(got) != 0 {
			t.Fatalf("Env() = %v, want empty", got)
		}
	})
	t.Run("Has", func(t *testing.T) {
		if s.Has(CapabilityScreen) {
			t.Fatal("Has() = true on a nil session")
		}
	})
	t.Run("Capability", func(t *testing.T) {
		if got := s.Capability(CapabilityScreen); got.Available || got.Requested {
			t.Fatalf("Capability() = %+v, want an unavailable status", got)
		}
	})
	t.Run("Capabilities", func(t *testing.T) {
		for _, status := range s.Capabilities() {
			if status.Available {
				t.Fatalf("Capabilities() reported %+v available", status)
			}
		}
	})
	t.Run("Close", func(t *testing.T) {
		if err := s.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	})
	t.Run("Launch", func(t *testing.T) {
		_, err := s.Launch(context.Background(), Command{Name: "true"})
		if !errors.Is(err, ErrNilSession) {
			t.Fatalf("Launch() = %v, want ErrNilSession", err)
		}
	})
	t.Run("Paste", func(t *testing.T) {
		if err := s.Paste(context.Background(), "text"); err == nil {
			t.Fatal("Paste() = nil, want an error")
		}
	})
	t.Run("Target", func(t *testing.T) {
		// Target has a value return, so it must return a usable zero value
		// rather than panic.
		_ = s.Target()
	})
	t.Run("Timeouts", func(t *testing.T) {
		// Timeouts must not panic; a zero policy is returned and callers apply
		// their defaults.
		_ = s.Timeouts().WithDefaults()
	})
}

// The facades are exported struct fields, so reading one through a nil Session
// dereferences the field and cannot be made safe. A nil facade reached any other
// way is the reachable case, and it reports the missing session rather than
// panicking.
func TestNilFacadesReportNilSession(t *testing.T) {
	ctx := context.Background()

	var screen *ScreenBundle
	if _, err := screen.Grab(ctx, image.Rectangle{}); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Screen.Grab() = %v, want ErrNilSession", err)
	}

	var input *InputBundle
	if err := input.Type(ctx, "x"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Input.Type() = %v, want ErrNilSession", err)
	}

	var windows *WindowBundle
	if _, err := windows.List(ctx, WindowMatch{}); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Windows.List() = %v, want ErrNilSession", err)
	}

	var clipboard *ClipboardBundle
	if _, err := clipboard.Get(ctx); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Clipboard.Get() = %v, want ErrNilSession", err)
	}

	var outputs *OutputBundle
	if _, err := outputs.List(ctx); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Outputs.List() = %v, want ErrNilSession", err)
	}
}
