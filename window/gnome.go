//go:build linux
// +build linux

package window

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

// GnomeManager implements window management for GNOME Shell via the
// org.gnome.Shell.Eval D-Bus interface.
type GnomeManager struct {
	conn *dbus.Conn
}

// NewGnomeManager opens a D-Bus connection and verifies that
// org.gnome.Shell.Eval is accessible.
func NewGnomeManager() (*GnomeManager, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("gnome: session bus: %w", err)
	}
	g := &GnomeManager{conn: conn}
	// Probe to ensure Eval works.
	_, err = g.eval(`"ok"`)
	if err != nil {
		return nil, fmt.Errorf("gnome: Shell Eval not available: %w", err)
	}
	return g, nil
}

// eval runs JavaScript in gnome-shell and returns the result string.
func (g *GnomeManager) eval(js string) (string, error) {
	obj := g.conn.Object("org.gnome.Shell", "/org/gnome/Shell")
	call := obj.Call("org.gnome.Shell.Eval", 0, js)
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

func (g *GnomeManager) List() ([]Info, error) {
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
        pid:   w.get_pid(),
        x:     r.x,
        y:     r.y,
        w:     r.width,
        h:     r.height
      };
    })
)`
	raw, err := g.eval(js)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		ID    uint64 `json:"id"`
		Title string `json:"title"`
		PID   int32  `json:"pid"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
		W     int    `json:"w"`
		H     int    `json:"h"`
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("gnome: list parse: %w", err)
	}
	out := make([]Info, len(entries))
	for i, e := range entries {
		out[i] = Info{ID: e.ID, Title: e.Title, PID: e.PID, X: e.X, Y: e.Y, W: e.W, H: e.H}
	}
	return out, nil
}

func (g *GnomeManager) findWindow(title string) string {
	lower := strings.ToLower(title)
	// Returns JS expression that evaluates to the Meta.Window or null.
	return fmt.Sprintf(`global.get_window_actors().map(a=>a.get_meta_window()).find(w=>(w.get_title()||"").toLowerCase().includes(%q))`, lower)
}

func (g *GnomeManager) actOnWindow(title, action string) error {
	js := fmt.Sprintf(`(function(){ let w=%s; if(!w) throw "not found"; %s; return "ok"; })()`, g.findWindow(title), action)
	_, err := g.eval(js)
	return err
}

func (g *GnomeManager) Activate(title string) error {
	return g.actOnWindow(title, `w.activate(global.get_current_time())`)
}

func (g *GnomeManager) Move(title string, x, y int) error {
	return g.actOnWindow(title, fmt.Sprintf(`w.move_frame(true, %d, %d)`, x, y))
}

func (g *GnomeManager) Resize(title string, w, h int) error {
	return g.actOnWindow(title, fmt.Sprintf(`w.move_resize_frame(true, w.get_frame_rect().x, w.get_frame_rect().y, %d, %d)`, w, h))
}

func (g *GnomeManager) ActiveTitle() (string, error) {
	js := `(function(){ let f=global.display.get_focus_window(); return f ? f.get_title() : ""; })()`
	return g.eval(js)
}

func (g *GnomeManager) CloseWindow(title string) error {
	return g.actOnWindow(title, `w.delete(global.get_current_time())`)
}

func (g *GnomeManager) Minimize(title string) error {
	return g.actOnWindow(title, `w.minimize()`)
}

func (g *GnomeManager) Maximize(title string) error {
	return g.actOnWindow(title, `w.maximize(3)`) // 3 = Meta.MaximizeFlags.BOTH
}

func (g *GnomeManager) Close() error {
	return nil
}
