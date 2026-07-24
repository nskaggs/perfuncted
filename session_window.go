package perfuncted

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// WindowHandle is an opaque identifier for a window. It wraps the window.Info
// ID and carries the title for backend calls that still require string matching.
type WindowHandle struct {
	id    uint64
	title string
}

// ID returns the numeric window identifier.
func (h WindowHandle) ID() uint64 { return h.id }

// Title returns the window title captured at list time.
func (h WindowHandle) Title() string { return h.title }

// Window wraps a WindowHandle with a back-reference to the session that
// created it. All operations delegate to the session's window backend.
type Window struct {
	handle  WindowHandle
	session *Session
}

// Handle returns the underlying WindowHandle.
func (w *Window) Handle() WindowHandle { return w.handle }

// Title returns the window title.
func (w *Window) Title() string { return w.handle.title }

// ID returns the window ID.
func (w *Window) ID() uint64 { return w.handle.id }

// Activate brings this window to focus.
func (w *Window) Activate(ctx context.Context) error {
	if w.session == nil || w.session.Windows == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	return w.session.Windows.Activate(ctx, w.handle.title)
}

// Move positions the window at (x, y).
func (w *Window) Move(ctx context.Context, x, y int) error {
	if w.session == nil || w.session.Windows == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	return w.session.Windows.Move(ctx, w.handle.title, x, y)
}

// Minimize minimizes the window.
func (w *Window) Minimize(ctx context.Context) error {
	if w.session == nil || w.session.Windows == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	return w.session.Windows.Minimize(ctx, w.handle.title)
}

// Maximize maximizes the window.
func (w *Window) Maximize(ctx context.Context) error {
	if w.session == nil || w.session.Windows == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	return w.session.Windows.Maximize(ctx, w.handle.title)
}

// ---------- List / Find ----------

// WindowListOptions filters the window listing.
type WindowListOptions struct {
	TitleContains string
	AppID         string
}

// List returns all windows matching the given options.
func (s *Session) List(ctx context.Context, opts ...WindowListOptions) ([]*Window, error) {
	if s == nil {
		return nil, ErrNilSession
	}
	if s.Windows == nil {
		return nil, &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}

	infos, err := s.Windows.List(ctx)
	if err != nil {
		return nil, err
	}

	var filter *WindowListOptions
	if len(opts) > 0 {
		filter = &opts[0]
	}

	var result []*Window
	for _, info := range infos {
		if filter != nil {
			if filter.TitleContains != "" && !strings.Contains(info.Title, filter.TitleContains) {
				continue
			}
			if filter.AppID != "" && info.AppID != filter.AppID {
				continue
			}
		}
		result = append(result, &Window{
			handle:  WindowHandle{id: info.ID, title: info.Title},
			session: s,
		})
	}
	return result, nil
}

// Find returns the first window matching the predicate.
func (s *Session) Find(ctx context.Context, pred func(*Window) bool) (*Window, error) {
	windows, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range windows {
		if pred(w) {
			return w, nil
		}
	}
	return nil, fmt.Errorf("window not found")
}

// FindByTitle returns the first window whose title contains the given string.
func (s *Session) FindByTitle(ctx context.Context, titleContains string) (*Window, error) {
	return s.Find(ctx, func(w *Window) bool {
		return strings.Contains(w.handle.title, titleContains)
	})
}

// FindByID returns the window with the given ID.
func (s *Session) FindByID(ctx context.Context, id uint64) (*Window, error) {
	return s.Find(ctx, func(w *Window) bool {
		return w.handle.id == id
	})
}

// ---------- Wait ----------

// Condition defines a wait predicate against window state.
type Condition struct {
	Name    string
	Match   func([]*Window) bool
	Timeout time.Duration
}

// All returns a Condition that succeeds when all children succeed.
func All(conditions ...Condition) Condition {
	return Condition{
		Name: "all",
		Match: func(windows []*Window) bool {
			for _, c := range conditions {
				if !c.Match(windows) {
					return false
				}
			}
			return true
		},
	}
}

// Any returns a Condition that succeeds when any child succeeds.
func Any(conditions ...Condition) Condition {
	return Condition{
		Name: "any",
		Match: func(windows []*Window) bool {
			for _, c := range conditions {
				if c.Match(windows) {
					return true
				}
			}
			return false
		},
	}
}

// Not returns a Condition that succeeds when the child fails.
func Not(c Condition) Condition {
	return Condition{
		Name: "not " + c.Name,
		Match: func(windows []*Window) bool {
			return !c.Match(windows)
		},
	}
}

// WaitOption configures the behavior of Wait.
type WaitOption func(*waitConfig)

type waitConfig struct {
	interval time.Duration
	timeout  time.Duration
}

func WaitEvery(d time.Duration) WaitOption {
	return func(c *waitConfig) { c.interval = d }
}

// WindowCondition returns a Condition that matches when there is at least one
// window whose title contains the given string.
func WindowCondition(titleContains string) Condition {
	return Condition{
		Name: fmt.Sprintf("window(%q)", titleContains),
		Match: func(windows []*Window) bool {
			for _, w := range windows {
				if strings.Contains(w.handle.title, titleContains) {
					return true
				}
			}
			return false
		},
	}
}

// WindowGoneCondition returns a Condition that matches when no window title
// contains the given string.
func WindowGoneCondition(titleContains string) Condition {
	return Not(WindowCondition(titleContains))
}

// Wait polls window state until the condition succeeds or the timeout expires.
func (s *Session) Wait(ctx context.Context, cond Condition, opts ...WaitOption) error {
	if s == nil {
		return ErrNilSession
	}

	cfg := waitConfig{
		interval: 100 * time.Millisecond,
		timeout:  cond.Timeout,
	}
	if cfg.timeout == 0 {
		cfg.timeout = 10 * time.Second
	}
	for _, o := range opts {
		o(&cfg)
	}

	deadline := time.After(cfg.timeout)
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()

	for {
		windows, err := s.List(ctx)
		if err == nil && cond.Match(windows) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("wait %s: timed out after %s", cond.Name, cfg.timeout)
		case <-ticker.C:
		}
	}
}

// ---------- invalidator (internal) ----------

// WindowInvalidator periodically refreshes cached window state. It is used
// internally by wait engines.
type WindowInvalidator struct {
	session  *Session
	interval time.Duration
	stopCh   chan struct{}
}

// NewWindowInvalidator creates an invalidator that polls window state at the
// given interval.
func NewWindowInvalidator(s *Session, interval time.Duration) *WindowInvalidator {
	return &WindowInvalidator{
		session:  s,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins background polling.
func (wi *WindowInvalidator) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(wi.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-wi.stopCh:
				return
			case <-ticker.C:
				wi.session.List(ctx)
			}
		}
	}()
}

// Stop halts background polling.
func (wi *WindowInvalidator) Stop() {
	close(wi.stopCh)
}

// WindowFromInfo creates a *Window from a window.Info result.
func WindowFromInfo(s *Session, info window.Info) *Window {
	return &Window{
		handle:  WindowHandle{id: info.ID, title: info.Title},
		session: s,
	}
}
