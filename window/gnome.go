//go:build linux
// +build linux

package window

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

var _ Manager = (*GnomeManager)(nil)

// gnomeShellService is the D-Bus service name for GNOME Shell's Eval interface.
const gnomeShellService = "org.gnome.Shell"

// GnomeManager implements window management for GNOME Shell via the
// org.gnome.Shell.Eval D-Bus interface.
type GnomeManager struct {
	conn *dbus.Conn
}

// NewGnomeManagerForBus opens a D-Bus connection at addr and verifies that
// org.gnome.Shell.Eval is accessible.
func NewGnomeManagerForBus(addr string) (*GnomeManager, error) {
	if addr == "" {
		return nil, fmt.Errorf("gnome: session bus unset")
	}
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("gnome: session bus: %w", err)
	}
	g := &GnomeManager{conn: conn}
	// Probe to ensure Eval works.
	_, err = g.eval(context.Background(), `"ok"`)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("gnome: Shell Eval not available: %w", err)
	}
	return g, nil
}

// eval runs JavaScript in gnome-shell and returns the result string.
func (g *GnomeManager) eval(ctx context.Context, js string) (string, error) {
	ctx = contextutil.Default(ctx)
	obj := g.conn.Object(gnomeShellService, "/org/gnome/Shell")
	call := obj.CallWithContext(ctx, "org.gnome.Shell.Eval", 0, js)
	if call.Err != nil {
		return "", call.Err
	}
	var success bool
	var result string
	if err := call.Store(&success, &result); err != nil {
		return "", err
	}
	if !success {
		return "", fmt.Errorf("gnome: eval failed: %s", result)
	}
	return result, nil
}

func (g *GnomeManager) List(ctx context.Context) ([]Info, error) {
	var out []Info
	for win, err := range g.IterateWindows(ctx) {
		if err != nil {
			return nil, err
		}
		out = append(out, win)
	}
	return out, nil
}

// IterateWindows returns an iterator over all visible top-level windows.
func (g *GnomeManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		const js = `
JSON.stringify(
  global.get_window_actors()
    .filter(a => !a.get_meta_window().is_skip_taskbar())
    .map(a => {
      let w = a.get_meta_window();
      let r = w.get_frame_rect();
      return {
        id:    w.get_stable_sequence(),
        title: w.get_title() || "",
        class: w.get_wm_class ? (w.get_wm_class() || "") : "",
        pid:   w.get_pid(),
        x:     r.x,
        y:     r.y,
        w:     r.width,
        h:     r.height
      };
    })
)`
		raw, err := g.eval(ctx, js)
		if err != nil {
			yield(Info{}, err)
			return
		}
		var entries []struct {
			ID    uint64 `json:"id"`
			Title string `json:"title"`
			Class string `json:"class"`
			PID   int32  `json:"pid"`
			X     int    `json:"x"`
			Y     int    `json:"y"`
			W     int    `json:"w"`
			H     int    `json:"h"`
		}
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			yield(Info{}, fmt.Errorf("gnome: list parse: %w", err))
			return
		}
		for _, e := range entries {
			if !yield(Info{
				ID:       e.ID,
				NativeID: strconv.FormatUint(e.ID, 10),
				Title:    e.Title,
				Class:    e.Class,
				PID:      e.PID,
				X:        e.X,
				Y:        e.Y,
				W:        e.W,
				H:        e.H,
			}, nil) {
				return
			}
		}
	}
}

func (g *GnomeManager) ActiveTitle(ctx context.Context) (string, error) {
	js := `(function(){ let f=global.display.get_focus_window(); return f ? f.get_title() : ""; })()`
	return g.eval(ctx, js)
}

func (g *GnomeManager) Close() error {
	if g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

func (g *GnomeManager) Sync(ctx context.Context) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("gnome: sync canceled: %w", err)
	}
	return nil
}

func (g *GnomeManager) SupportedOperations() []string {
	return []string{
		"discover",
		"activate",
		"move",
		"resize",
		"close",
		"minimize",
		"maximize",
		"restore",
	}
}

// --- Handle-based operations ---

func (g *GnomeManager) findWindowByID(id uint64) string {
	return fmt.Sprintf(`global.get_window_actors().map(a=>a.get_meta_window()).find(w=>w.get_stable_sequence()===%d)`, id)
}

func (g *GnomeManager) actOnWindowByID(ctx context.Context, id string, action string) error {
	numeric, err := numericID(id)
	if err != nil {
		return err
	}
	js := "(function(){ let w=" + g.findWindowByID(numeric) + "; if(!w) throw \"not found\"; " + action + "; return \"ok\"; })()"
	_, err = g.eval(ctx, js)
	return err
}

func (g *GnomeManager) ActivateByID(ctx context.Context, id string) error {
	return g.actOnWindowByID(ctx, id, `w.activate(global.get_current_time())`)
}

func (g *GnomeManager) MoveByID(ctx context.Context, id string, x, y int) error {
	return g.actOnWindowByID(ctx, id, "w.move_frame(true, "+strconv.Itoa(x)+", "+strconv.Itoa(y)+")")
}

func (g *GnomeManager) ResizeByID(ctx context.Context, id string, w, h int) error {
	return g.actOnWindowByID(ctx, id, "w.move_resize_frame(true, w.get_frame_rect().x, w.get_frame_rect().y, "+strconv.Itoa(w)+", "+strconv.Itoa(h)+")")
}

func (g *GnomeManager) CloseWindowByID(ctx context.Context, id string) error {
	return g.actOnWindowByID(ctx, id, `w.delete(global.get_current_time())`)
}

func (g *GnomeManager) MinimizeByID(ctx context.Context, id string) error {
	return g.actOnWindowByID(ctx, id, `w.minimize()`)
}

func (g *GnomeManager) MaximizeByID(ctx context.Context, id string) error {
	return g.actOnWindowByID(ctx, id, `w.maximize(3)`)
}

func (g *GnomeManager) FullscreenByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (g *GnomeManager) UnfullscreenByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (g *GnomeManager) RestoreByID(ctx context.Context, id string) error {
	return g.actOnWindowByID(ctx, id, `w.unminimize(); w.unmaximize(3)`)
}

func (g *GnomeManager) InfoByID(ctx context.Context, id string) (Info, error) {
	for info, err := range g.IterateWindows(ctx) {
		if err != nil {
			return Info{}, err
		}
		if info.StableID() == id {
			return info, nil
		}
	}
	return Info{}, ErrWindowNotFound
}
