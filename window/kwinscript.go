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
	"iter"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/ctxutil"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

var _ Manager = (*KWinScriptManager)(nil)

const (
	kwinScriptSvc   = "org.kde.KWin"
	kwinScriptPath  = dbus.ObjectPath("/Scripting")
	kwinScriptIface = "org.kde.kwin.Scripting"
)

// KWinScriptManager implements Manager for KDE Plasma Wayland.
type KWinScriptManager struct {
	conn *dbus.Conn
}

// NewKWinScriptManagerForBus returns a KWinScriptManager for the session bus
// at addr if the KWin scripting interface is accessible.
func NewKWinScriptManagerForBus(addr string) (*KWinScriptManager, error) {
	if addr == "" {
		return nil, fmt.Errorf("window/kwinscript: D-Bus session unset")
	}
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
func (k *KWinScriptManager) runScript(ctx context.Context, buildJS func(svc string) string) (string, error) {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if k == nil || k.conn == nil {
		return "", fmt.Errorf("window/kwinscript: backend not initialised")
	}

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
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case data := <-recv.ch:
		return data, nil
	case <-timer.C:
		return "", fmt.Errorf("window/kwinscript: timeout — script %d did not call back (is KWin scripting enabled?)", scriptID)
	}
}

func parseKWinWindowList(data string) []Info {
	var infos []Info
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 9)
		var info Info
		if len(parts) >= 1 {
			info.NativeID = strings.TrimSpace(parts[0])
			if id, err := strconv.ParseUint(info.NativeID, 0, 64); err == nil {
				info.ID = id
			}
		}
		if len(parts) >= 2 {
			info.Title = parts[1]
		}
		if len(parts) >= 3 {
			info.AppID = parts[2]
		}
		if len(parts) >= 4 {
			info.Class = parts[3]
		}
		if len(parts) >= 5 {
			if pid, err := strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 32); err == nil {
				info.PID = int32(pid)
			}
		}
		if len(parts) >= 9 {
			info.X = parseInt(parts[5])
			info.Y = parseInt(parts[6])
			info.W = parseInt(parts[7])
			info.H = parseInt(parts[8])
		}
		infos = append(infos, info)
	}
	return infos
}

func (k *KWinScriptManager) List(ctx context.Context) ([]Info, error) {
	var out []Info
	for win, err := range k.IterateWindows(ctx) {
		if err != nil {
			return nil, err
		}
		out = append(out, win)
	}
	return out, nil
}

// IterateWindows returns an iterator over all top-level windows.
func (k *KWinScriptManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		data, err := k.runScript(ctx, func(svc string) string {
			return "\nvar listFunc = (typeof workspace.windowList === \"function\") ? workspace.windowList : workspace.clientList;\nvar wins = listFunc();\nvar lines = [];\nfor (var i = 0; i < wins.length; i++) {\n    var w = wins[i];\n    if (w.normalWindow) {\n        var g = w.frameGeometry;\n        var id = (typeof w.internalId !== 'undefined') ? w.internalId : w.windowId;\n        lines.push(id + '\\t' + w.caption + '\\t' + (w.resourceName || '')\n            + '\\t' + (w.resourceClass || '') + '\\t' + w.pid\n            + '\\t' + g.x + '\\t' + g.y + '\\t' + g.width + '\\t' + g.height);\n    }\n}\ncallDBus('" + svc + "', '/', '" + svc + "', 'ReportWindows', lines.join('\\n'));\n"
		})
		if err != nil {
			yield(Info{}, err)
			return
		}

		for _, info := range parseKWinWindowList(data) {
			if !yield(info, nil) {
				return
			}
		}
	}
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

const kwinScriptErrorPrefix = "__pf_error__:"

// ActiveTitle returns the caption of the currently focused window.
func (k *KWinScriptManager) ActiveTitle(ctx context.Context) (string, error) {
	return k.runScript(ctx, func(svc string) string {
		return "\nvar w = (typeof workspace.activeWindow !== 'undefined') ? workspace.activeWindow : workspace.activeClient;\ncallDBus('" + svc + "', '/', '" + svc + "', 'ReportWindows', w ? w.caption : '');\n"
	})
}

// Close is a no-op; the session bus connection is shared and managed globally.
func (k *KWinScriptManager) Close() error { return nil }

func (k *KWinScriptManager) Sync(ctx context.Context) error {
	return nil
}

func (k *KWinScriptManager) SupportedOperations() []string {
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

func kwinFindByIDScript(id, svc, actionJS string) string {
	return fmt.Sprintf(`
var listFunc = (typeof workspace.windowList === "function") ? workspace.windowList : workspace.clientList;
var wins = listFunc();
var found = '';
var targetId = %s;
try {
    for (var i = 0; i < wins.length; i++) {
        var w = wins[i];
        var wid = (typeof w.internalId !== 'undefined') ? w.internalId : w.windowId;
        if (String(wid) === targetId) {
            found = w.caption;
            %s
            break;
        }
    }
} catch(e) {
    found = %q + String(e);
}
callDBus('%s', '/', '%s', 'ReportWindows', found);
`, strconv.Quote(id), actionJS, kwinScriptErrorPrefix, svc, svc)
}

func kwinActionResultByID(id, result string) error {
	if strings.HasPrefix(result, kwinScriptErrorPrefix) {
		return fmt.Errorf(
			"window/kwinscript: action on id=%q failed: %s",
			id,
			strings.TrimPrefix(result, kwinScriptErrorPrefix),
		)
	}
	if result == "" {
		return ErrWindowNotFound
	}
	return nil
}

func (k *KWinScriptManager) ActivateByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc,
			"w.minimized = false;\n            (typeof workspace.activateWindow === \"function\") ? workspace.activateWindow(w) : workspace.activeClient = w;")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func (k *KWinScriptManager) MoveByID(ctx context.Context, id string, x, y int) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, kwinMoveAction(x, y))
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func kwinMoveAction(x, y int) string {
	return fmt.Sprintf(
		"var g = w.frameGeometry;\n            w.frameGeometry = {x: %d, y: %d, width: Math.round(g.width), height: Math.round(g.height)};",
		x, y)
}

func (k *KWinScriptManager) ResizeByID(ctx context.Context, id string, width, height int) error {
	result, err := k.runScript(ctx, func(svc string) string {
		action := fmt.Sprintf(
			"var g = w.frameGeometry;\n            w.frameGeometry = {x: Math.round(g.x), y: Math.round(g.y), width: %d, height: %d};",
			width, height)
		return kwinFindByIDScript(id, svc, action)
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func (k *KWinScriptManager) CloseWindowByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.closeWindow();")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func (k *KWinScriptManager) MinimizeByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.minimized = true;")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func (k *KWinScriptManager) MaximizeByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.setMaximize(true, true);")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func (k *KWinScriptManager) FullscreenByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (k *KWinScriptManager) UnfullscreenByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (k *KWinScriptManager) RestoreByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.setMaximize(false, false); w.minimized = false;")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

func (k *KWinScriptManager) InfoByID(ctx context.Context, id string) (Info, error) {
	for info, err := range k.IterateWindows(ctx) {
		if err != nil {
			return Info{}, err
		}
		if info.StableID() == id {
			return info, nil
		}
	}
	return Info{}, ErrWindowNotFound
}

func (k *KWinScriptManager) WaitClosedByID(ctx context.Context, id string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := k.InfoByID(ctx, id); err != nil {
				return nil
			}
		}
	}
}
