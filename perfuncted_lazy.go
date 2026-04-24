// Code for lazy backend wrappers to support deferred initialization.
package perfuncted

import (
	"context"
	"sync"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/window"
)

// lazyInputter defers input.Open() until needed.
type lazyInputter struct {
	mu   sync.Mutex
	env  sessionEnv
	maxX int32
	maxY int32
	real input.Inputter
}

func (l *lazyInputter) ensure() error { // intentionally misspelled to avoid shadow warnings in editors
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return nil
	}
	restore := applySessionEnv(l.env)
	defer restore()
	in, err := input.Open(l.maxX, l.maxY)
	if err != nil {
		return err
	}
	l.real = in
	return nil
}

// Ensure wrapper name matches method receiver names below.
func (l *lazyInputter) ensureReal() error {
	// wrapper so callers use a stable method name
	return l.ensure()
}

func (l *lazyInputter) KeyDown(ctx context.Context, key string) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.KeyDown(ctx, key)
}
func (l *lazyInputter) KeyUp(ctx context.Context, key string) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.KeyUp(ctx, key)
}
func (l *lazyInputter) KeyTap(ctx context.Context, key string) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.KeyTap(ctx, key)
}
func (l *lazyInputter) Type(ctx context.Context, s string) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.Type(ctx, s)
}
func (l *lazyInputter) MouseMove(ctx context.Context, x, y int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.MouseMove(ctx, x, y)
}
func (l *lazyInputter) MouseClick(ctx context.Context, x, y, button int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.MouseClick(ctx, x, y, button)
}
func (l *lazyInputter) MouseDown(ctx context.Context, button int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.MouseDown(ctx, button)
}
func (l *lazyInputter) MouseUp(ctx context.Context, button int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.MouseUp(ctx, button)
}
func (l *lazyInputter) ScrollUp(ctx context.Context, clicks int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.ScrollUp(ctx, clicks)
}
func (l *lazyInputter) ScrollDown(ctx context.Context, clicks int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.ScrollDown(ctx, clicks)
}
func (l *lazyInputter) ScrollLeft(ctx context.Context, clicks int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.ScrollLeft(ctx, clicks)
}
func (l *lazyInputter) ScrollRight(ctx context.Context, clicks int) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.ScrollRight(ctx, clicks)
}
func (l *lazyInputter) PressCombo(ctx context.Context, combo string) error {
	if err := l.ensureReal(); err != nil {
		return err
	}
	return l.real.PressCombo(ctx, combo)
}
func (l *lazyInputter) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return l.real.Close()
	}
	return nil
}

// lazyWindowManager defers window.Open()
type lazyWindowManager struct {
	mu   sync.Mutex
	env  sessionEnv
	real window.Manager
}

func (l *lazyWindowManager) ensure() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return nil
	}
	restore := applySessionEnv(l.env)
	defer restore()
	m, err := window.Open()
	if err != nil {
		return err
	}
	l.real = m
	return nil
}

func (l *lazyWindowManager) List(ctx context.Context) ([]window.Info, error) {
	if err := l.ensure(); err != nil {
		return nil, err
	}
	return l.real.List(ctx)
}
func (l *lazyWindowManager) Activate(ctx context.Context, title string) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.Activate(ctx, title)
}
func (l *lazyWindowManager) Move(ctx context.Context, title string, x, y int) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.Move(ctx, title, x, y)
}
func (l *lazyWindowManager) Resize(ctx context.Context, title string, w, h int) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.Resize(ctx, title, w, h)
}
func (l *lazyWindowManager) ActiveTitle(ctx context.Context) (string, error) {
	if err := l.ensure(); err != nil {
		return "", err
	}
	return l.real.ActiveTitle(ctx)
}
func (l *lazyWindowManager) CloseWindow(ctx context.Context, title string) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.CloseWindow(ctx, title)
}
func (l *lazyWindowManager) Minimize(ctx context.Context, title string) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.Minimize(ctx, title)
}
func (l *lazyWindowManager) Maximize(ctx context.Context, title string) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.Maximize(ctx, title)
}
func (l *lazyWindowManager) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return l.real.Close()
	}
	return nil
}

// lazyClipboard defers clipboard.Open()
type lazyClipboard struct {
	mu   sync.Mutex
	env  sessionEnv
	real clipboard.Clipboard
}

func (l *lazyClipboard) ensure() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return nil
	}
	restore := applySessionEnv(l.env)
	defer restore()
	b, err := clipboard.Open()
	if err != nil {
		return err
	}
	// clipboard.Open returns a Bundle which implements clipboard.Clipboard
	l.real = b
	return nil
}

func (l *lazyClipboard) Get(ctx context.Context) (string, error) {
	if err := l.ensure(); err != nil {
		return "", err
	}
	return l.real.Get(ctx)
}
func (l *lazyClipboard) Set(ctx context.Context, text string) error {
	if err := l.ensure(); err != nil {
		return err
	}
	return l.real.Set(ctx, text)
}
func (l *lazyClipboard) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.real != nil {
		return l.real.Close()
	}
	return nil
}
