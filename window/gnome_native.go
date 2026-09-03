//go:build linux
// +build linux

package window

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"sync"

	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

var _ IDManager = (*GnomeNativeManager)(nil)

// GnomeNativeManager uses Mutter's typed window API exposed by the bundled
// bridge; it never sends JavaScript or otherwise evaluates Shell code.
type GnomeNativeManager struct {
	bridge       *gnomebridge.Client
	events       chan Event
	stopEvents   chan struct{}
	eventsDone   chan struct{}
	stopEventsMu sync.Once
}

// NewGnomeNativeManagerForRuntime connects to the native GNOME bridge.
func NewGnomeNativeManagerForRuntime(rt env.Runtime) (*GnomeNativeManager, error) {
	bridge, err := gnomebridge.ConnectForCapability(context.Background(), rt, gnomebridge.CapabilityWindows)
	if err != nil {
		return nil, fmt.Errorf("window/gnome-native: %w", err)
	}
	bridgeEvents, cancelEvents, err := bridge.SubscribeWindowEvents(context.Background())
	if err != nil {
		_ = bridge.Close()
		return nil, fmt.Errorf("window/gnome-native: subscribe to window events: %w", err)
	}
	m := &GnomeNativeManager{
		bridge:     bridge,
		events:     make(chan Event, 64),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go m.forwardWindowEvents(bridgeEvents, cancelEvents)
	return m, nil
}

func (m *GnomeNativeManager) forwardWindowEvents(events <-chan gnomebridge.WindowEvent, cancel func()) {
	defer close(m.eventsDone)
	defer close(m.events)
	defer cancel()
	for {
		select {
		case <-m.stopEvents:
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			converted := Event{ID: event.ID}
			switch event.Kind {
			case gnomebridge.WindowAddedEvent:
				converted.Kind = WindowAddedEvent
				converted.Window = toWindowInfo(event.Window)
				if converted.ID == "" {
					converted.ID = event.Window.ID
				}
			case gnomebridge.WindowRemovedEvent:
				converted.Kind = WindowRemovedEvent
			case gnomebridge.WindowChangedEvent:
				converted.Kind = WindowChangedEvent
				converted.Window = toWindowInfo(event.Window)
				if converted.ID == "" {
					converted.ID = event.Window.ID
				}
			case gnomebridge.FocusChangedEvent:
				converted.Kind = FocusChangedEvent
			default:
				continue
			}
			select {
			case <-m.stopEvents:
				return
			default:
			}
			select {
			case m.events <- converted:
			case <-m.stopEvents:
				return
			default:
				// Delivery is intentionally lossy here. The bridge has already
				// coalesced pending changes, and a slow or unused optional event
				// consumer must never block D-Bus dispatch.
			}
		}
	}
}

// WindowEvents returns native GNOME window lifecycle and focus events. The
// bounded stream is lossy for slow consumers, including for lifecycle and
// focus events, but it cannot block D-Bus dispatch.
func (m *GnomeNativeManager) WindowEvents() <-chan Event {
	if m == nil {
		return nil
	}
	return m.events
}

func toWindowInfo(window gnomebridge.WindowInfo) Info {
	var id uint64
	if parsed, err := strconv.ParseUint(window.ID, 10, 64); err == nil {
		id = parsed
	}
	return Info{
		ID:         id,
		NativeID:   window.ID,
		Title:      window.Title,
		AppID:      window.AppID,
		Class:      window.Class,
		PID:        window.PID,
		X:          int(window.X),
		Y:          int(window.Y),
		W:          int(window.Width),
		H:          int(window.Height),
		Active:     window.Active,
		Minimized:  window.Minimized,
		Maximized:  window.Maximized,
		Fullscreen: window.Fullscreen,
	}
}

// List returns the visible GNOME windows.
func (m *GnomeNativeManager) List(ctx context.Context) ([]Info, error) {
	windows, err := m.bridge.ListWindows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(windows))
	for _, window := range windows {
		out = append(out, toWindowInfo(window))
	}
	return out, nil
}

// IterateWindows returns an iterator over visible GNOME windows.
func (m *GnomeNativeManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		windows, err := m.List(ctx)
		if err != nil {
			yield(Info{}, err)
			return
		}
		for _, window := range windows {
			if !yield(window, nil) {
				return
			}
		}
	}
}

// ActiveTitle returns the title of the focused GNOME window.
func (m *GnomeNativeManager) ActiveTitle(ctx context.Context) (string, error) {
	window, err := m.bridge.GetActiveWindow(ctx)
	if err != nil {
		return "", err
	}
	return window.Title, nil
}

// Sync verifies that the GNOME bridge is responsive.
func (m *GnomeNativeManager) Sync(ctx context.Context) error {
	ctx = contextutil.Default(ctx)
	return m.bridge.Ping(ctx)
}

func (m *GnomeNativeManager) action(ctx context.Context, action func(context.Context) error) error {
	err := action(contextutil.Default(ctx))
	if errors.Is(err, gnomebridge.ErrObjectNotFound) {
		return ErrWindowNotFound
	}
	return err
}

// ActivateByID activates the GNOME window identified by id.
func (m *GnomeNativeManager) ActivateByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.Activate(ctx, id) })
}

// MoveByID moves the GNOME window identified by id.
func (m *GnomeNativeManager) MoveByID(ctx context.Context, id string, x, y int) error {
	return m.action(ctx, func(ctx context.Context) error {
		if err := validateGnomeInt32Values("move", x, y); err != nil {
			return err
		}
		return m.bridge.Move(ctx, id, int32(x), int32(y))
	})
}

// ResizeByID resizes the GNOME window identified by id.
func (m *GnomeNativeManager) ResizeByID(ctx context.Context, id string, width, height int) error {
	return m.action(ctx, func(ctx context.Context) error {
		if err := validateGnomeInt32Values("resize", width, height); err != nil {
			return err
		}
		return m.bridge.Resize(ctx, id, int32(width), int32(height))
	})
}

func validateGnomeInt32Values(operation string, values ...int) error {
	for _, value := range values {
		if value < -1<<31 || value > 1<<31-1 {
			return fmt.Errorf("window/gnome-native: %s value %d exceeds int32 range", operation, value)
		}
	}
	return nil
}

// CloseWindowByID closes the GNOME window identified by id.
func (m *GnomeNativeManager) CloseWindowByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.CloseWindow(ctx, id) })
}

// MinimizeByID minimizes the GNOME window identified by id.
func (m *GnomeNativeManager) MinimizeByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.Minimize(ctx, id) })
}

// MaximizeByID maximizes the GNOME window identified by id.
func (m *GnomeNativeManager) MaximizeByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.Maximize(ctx, id) })
}

// FullscreenByID makes the GNOME window identified by id fullscreen.
func (m *GnomeNativeManager) FullscreenByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.Fullscreen(ctx, id) })
}

// UnfullscreenByID exits fullscreen for the GNOME window identified by id.
func (m *GnomeNativeManager) UnfullscreenByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.Unfullscreen(ctx, id) })
}

// RestoreByID restores the GNOME window identified by id.
func (m *GnomeNativeManager) RestoreByID(ctx context.Context, id string) error {
	return m.action(ctx, func(ctx context.Context) error { return m.bridge.Restore(ctx, id) })
}

// InfoByID returns fresh information for a GNOME window.
func (m *GnomeNativeManager) InfoByID(ctx context.Context, id string) (Info, error) {
	window, err := m.bridge.GetWindow(ctx, id)
	if errors.Is(err, gnomebridge.ErrObjectNotFound) {
		return Info{}, ErrWindowNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return toWindowInfo(window), nil
}

// SupportedOperations reports the operations supported by the GNOME backend.
func (m *GnomeNativeManager) SupportedOperations() []string {
	return append(capability.Operations("windows"), "events")
}

// Close releases the GNOME bridge connection.
func (m *GnomeNativeManager) Close() error {
	if m == nil || m.bridge == nil {
		return nil
	}
	m.stopEventsMu.Do(func() { close(m.stopEvents) })
	if m.eventsDone != nil {
		<-m.eventsDone
	}
	return m.bridge.Close()
}
