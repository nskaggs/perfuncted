// KWin scripting backend for KDE Plasma Wayland.
//
// On KDE Wayland, neither ext_foreign_toplevel_list_v1 nor
// zwlr_foreign_toplevel_manager_v1 is advertised. The compositor's scripting
// engine (org.kde.kwin.Scripting) is the only API that exposes the full
// internal window model, including native Wayland windows invisible to EWMH.
//
// Each operation:
//  1. Registers a PID-scoped temporary D-Bus name so the script can call back.
//  2. Writes a small JS snippet to a temp file.
//  3. Calls org.kde.kwin.Scripting.loadScript — KWin runs it inside the compositor.
//  4. The script delivers data via callDBus to our registered ReportWindows method.
//  5. We parse, unregister, and delete the temp file.
//
// KWin scripts run inside the compositor process with no user consent dialog.
// This is KDE's official, intended automation interface.
//go:build linux
// +build linux

package window

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

const (
	kwinScriptSvc   = "org.kde.KWin"
	kwinScriptPath  = dbus.ObjectPath("/Scripting")
	kwinScriptIface = "org.kde.kwin.Scripting"
)

// KWinScriptManager implements Manager for KDE Plasma Wayland.
type KWinScriptManager struct {
	conn *dbus.Conn
}

// NewKWinScriptManager returns a KWinScriptManager if the KWin scripting
// interface is accessible on the session bus.
func NewKWinScriptManager() (*KWinScriptManager, error) {
	return NewKWinScriptManagerForBus("")
}

// NewKWinScriptManagerForBus returns a KWinScriptManager for the session bus
// at addr if the KWin scripting interface is accessible.
func NewKWinScriptManagerForBus(addr string) (*KWinScriptManager, error) {
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("window/kwinscript: D-Bus: %w", err)
	}
	var intro string
	obj := conn.Object(kwinScriptSvc, kwinScriptPath)
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&intro); err != nil {
		return nil, fmt.Errorf("window/kwinscript: KWin Scripting not on session bus: %w", err)
	}
	if !strings.Contains(intro, kwinScriptIface) {
		return nil, fmt.Errorf("window/kwinscript: %s interface absent", kwinScriptIface)
	}
	return &KWinScriptManager{conn: conn}, nil
}

// pfReceiver is the temporary D-Bus object that KWin scripts call back into.
type pfReceiver struct{ ch chan string }

func (r *pfReceiver) ReportWindows(data string) *dbus.Error {
	select {
	case r.ch <- data:
	default:
	}
	return nil
}

// runScript registers a temporary D-Bus name, writes js to a temp file, loads
// it into KWin, waits for the script to call ReportWindows, and returns the
// delivered string. Cleans up the name and file on return.
//
// The JS snippet must contain exactly one callDBus call:
//
//	callDBus(svc, '/', svc, 'ReportWindows', <result string>);
//
// where svc is the value passed to buildJS.
func (k *KWinScriptManager) runScript(buildJS func(svc string) string) (string, error) {
	svc := fmt.Sprintf("org.kde.pflist%d", os.Getpid())

	reply, err := k.conn.RequestName(svc, dbus.NameFlagDoNotQueue)
	if err != nil {
		return "", fmt.Errorf("window/kwinscript: RequestName: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return "", fmt.Errorf("window/kwinscript: D-Bus name %s already taken", svc)
	}
	defer k.conn.ReleaseName(svc) //nolint:errcheck

	recv := &pfReceiver{ch: make(chan string, 1)}
	err = k.conn.Export(recv, "/", svc)
	if err != nil {
		return "", fmt.Errorf("window/kwinscript: Export: %w", err)
	}
	defer k.conn.Export(nil, "/", svc) //nolint:errcheck

	f, err := os.CreateTemp("", "pf-kwin-*.js")
	if err != nil {
		return "", fmt.Errorf("window/kwinscript: temp file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(buildJS(svc)); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	scr := k.conn.Object(kwinScriptSvc, kwinScriptPath)
	var scriptID int
	if err := scr.Call(kwinScriptIface+".loadScript", 0, f.Name()).Store(&scriptID); err != nil {
		return "", fmt.Errorf("window/kwinscript: loadScript: %w", err)
	}
	// start() triggers the scripting engine to execute loaded scripts.
	// Without this call the script is registered but never runs.
	scr.Call(kwinScriptIface+".start", 0) //nolint:errcheck

	timer := time.NewTimer(5 * time.Second)
	select {
	case data := <-recv.ch:
		timer.Stop()
		return data, nil
	case <-timer.C:
		return "", fmt.Errorf("window/kwinscript: timeout — script %d did not call back (is KWin scripting enabled?)", scriptID)
	}
}

// List returns all normal top-level windows reported by workspace.windowList().
func (k *KWinScriptManager) List(ctx context.Context) ([]Info, error) {
	data, err := k.runScript(func(svc string) string {
		return fmt.Sprintf(`
var wins = workspace.windowList();
var lines = [];
for (var i = 0; i < wins.length; i++) {
    var w = wins[i];
    if (w.normalWindow) {
        var g = w.frameGeometry;
        lines.push(w.internalId + '\t' + w.caption + '\t' + w.pid
            + '\t' + g.x + '\t' + g.y + '\t' + g.width + '\t' + g.height);
    }
}
callDBus('%s', '/', '%s', 'ReportWindows', lines.join('\n'));
`, svc, svc)
	})
	if err != nil {
		return nil, err
	}

	var infos []Info
	for i, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 7)
		info := Info{ID: uint64(i + 1)}
		if len(parts) >= 2 {
			info.Title = parts[1]
		}
		if len(parts) >= 3 {
			if pid, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 32); err == nil {
				info.PID = int32(pid)
			}
		}
		if len(parts) >= 7 {
			info.X = parseInt(parts[3])
			info.Y = parseInt(parts[4])
			info.W = parseInt(parts[5])
			info.H = parseInt(parts[6])
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func parseInt(s string) int {
	s = strings.TrimSpace(s)
	// KWin 6 returns QRectF values as floats (e.g. "856.6666666667");
	// try Atoi first (fast path), fall back to ParseFloat.
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return 0
}

// kwinFindWindowScript generates JS that iterates workspace.windowList(),
// finds the first window whose lowercased caption contains `safe`, runs
// actionJS on it, and calls back via callDBus with the matched caption
// (or empty string if not found). `safe` must already be JS-safe (escaped).
func kwinFindWindowScript(safe, svc, actionJS string) string {
	return fmt.Sprintf(`
var wins = workspace.windowList();
var found = '';
try {
    for (var i = 0; i < wins.length; i++) {
        var w = wins[i];
        if (w.caption.toLowerCase().indexOf('%s') !== -1) {
            found = w.caption;
            %s
            break;
        }
    }
} catch(e) {}
callDBus('%s', '/', '%s', 'ReportWindows', found);
`, safe, actionJS, svc, svc)
}

// Activate raises and focuses the first window whose title contains substr.
func (k *KWinScriptManager) Activate(ctx context.Context, title string) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		return kwinFindWindowScript(safe, svc,
			"w.minimized = false;\n            workspace.activateWindow(w);")
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}

// Restore restores the first window whose title contains substr.
func (k *KWinScriptManager) Restore(ctx context.Context, title string) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		return kwinFindWindowScript(safe, svc, "w.setMaximize(false, false); w.minimized = false;")
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}

// ActiveTitle returns the caption of the currently focused window.
func (k *KWinScriptManager) ActiveTitle(ctx context.Context) (string, error) {
	return k.runScript(func(svc string) string {
		return fmt.Sprintf(`
var w = workspace.activeWindow;
callDBus('%s', '/', '%s', 'ReportWindows', w ? w.caption : '');
`, svc, svc)
	})
}

// Move repositions the first window whose title contains substr via KWin scripting.
func (k *KWinScriptManager) Move(ctx context.Context, title string, x, y int) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		action := fmt.Sprintf(
			"var g = w.frameGeometry;\n            w.frameGeometry = {x: %d, y: %d, width: Math.round(g.width), height: Math.round(g.height)};",
			x, y)
		return kwinFindWindowScript(safe, svc, action)
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}

// Resize changes the dimensions of the first window whose title contains substr via KWin scripting.
func (k *KWinScriptManager) Resize(ctx context.Context, title string, w, h int) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		action := fmt.Sprintf(
			"var g = w.frameGeometry;\n            w.frameGeometry = {x: Math.round(g.x), y: Math.round(g.y), width: %d, height: %d};",
			w, h)
		return kwinFindWindowScript(safe, svc, action)
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}

// Close is a no-op; the session bus connection is shared and managed globally.
func (k *KWinScriptManager) Close() error { return nil }

// CloseWindow closes the first window whose title contains substr.
func (k *KWinScriptManager) CloseWindow(ctx context.Context, title string) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		return kwinFindWindowScript(safe, svc, "w.closeWindow();")
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}

// Minimize minimizes the first window whose title contains substr.
func (k *KWinScriptManager) Minimize(ctx context.Context, title string) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		return kwinFindWindowScript(safe, svc, "w.minimized = true;")
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}

// Maximize maximizes the first window whose title contains substr.
func (k *KWinScriptManager) Maximize(ctx context.Context, title string) error {
	safe := strings.ReplaceAll(strings.ToLower(title), "'", "\\'")
	result, err := k.runScript(func(svc string) string {
		return kwinFindWindowScript(safe, svc, "w.setMaximize(true, true);")
	})
	if err != nil {
		return err
	}
	if result == "" {
		return fmt.Errorf("window: window matching %q not found", title)
	}
	return nil
}
