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
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

var _ Manager = (*KWinScriptManager)(nil)

const (
	kwinScriptSvc   = "org.kde.KWin"
	kwinScriptPath  = dbus.ObjectPath("/Scripting")
	kwinScriptIface = "org.kde.kwin.Scripting"
)

var kwinScriptSequence uint64

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
		_ = conn.Close()
		return nil, fmt.Errorf("window/kwinscript: KWin Scripting not on session bus: %w", err)
	}
	if !strings.Contains(intro, kwinScriptIface) {
		_ = conn.Close()
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
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if k == nil || k.conn == nil {
		return "", fmt.Errorf("window/kwinscript: backend not initialised")
	}

	svc := fmt.Sprintf("org.kde.pflist%d_%d", os.Getpid(), atomic.AddUint64(&kwinScriptSequence, 1))

	var rawReply uint32
	if err := k.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.RequestName", 0, svc, dbus.NameFlagDoNotQueue).Store(&rawReply); err != nil {
		return "", fmt.Errorf("window/kwinscript: RequestName: %w", err)
	}
	reply := dbus.RequestNameReply(rawReply)
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return "", fmt.Errorf("window/kwinscript: D-Bus name %s already taken", svc)
	}
	defer k.conn.ReleaseName(svc) //nolint:errcheck

	recv := &pfReceiver{ch: make(chan string, 1)}
	err := k.conn.Export(recv, "/", svc)
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
		return "", errors.Join(
			fmt.Errorf("window/kwinscript: write temp file: %w", err),
			f.Close(),
		)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("window/kwinscript: close temp file: %w", err)
	}

	scr := k.conn.Object(kwinScriptSvc, kwinScriptPath)
	// loadScript registers the script inside KWin's scripting engine for the
	// rest of the compositor's lifetime; deleting the temp file does not
	// remove it. Use an explicit unique plugin name so the deferred
	// unloadScript below can find and remove it again — without this, every
	// operation leaks one loaded script (and polling callers accumulate
	// thousands of them).
	plugin := fmt.Sprintf("pf_%d_%d", os.Getpid(), atomic.AddUint64(&kwinScriptSequence, 1))
	var scriptID int
	if err := scr.CallWithContext(ctx, kwinScriptIface+".loadScript", 0, f.Name(), plugin).Store(&scriptID); err != nil {
		return "", fmt.Errorf("window/kwinscript: loadScript: %w", err)
	}
	defer func() {
		// Detach from the caller's context so cleanup still runs after a
		// cancellation or deadline, but bound it so a wedged bus cannot
		// stall the caller.
		unloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := scr.CallWithContext(unloadCtx, kwinScriptIface+".unloadScript", 0, plugin).Err; err != nil {
			slog.Debug("window/kwinscript: unloadScript failed", "plugin", plugin, "scriptID", scriptID, "error", err)
		}
	}()
	// start() triggers the scripting engine to execute loaded scripts.
	// Without this call the script is registered but never runs.
	if err := scr.CallWithContext(ctx, kwinScriptIface+".start", 0).Err; err != nil {
		return "", fmt.Errorf("window/kwinscript: start: %w", err)
	}

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

type kwinWindowRow struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	AppID  string  `json:"app_id"`
	Class  string  `json:"class"`
	PID    int32   `json:"pid"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func parseKWinWindowList(data string) ([]Info, error) {
	var rows []kwinWindowRow
	if err := json.Unmarshal([]byte(data), &rows); err != nil {
		return nil, fmt.Errorf("window/kwinscript: parse window list: %w", err)
	}
	infos := make([]Info, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" {
			return nil, fmt.Errorf("window/kwinscript: window list contains empty ID")
		}
		info := Info{
			NativeID: row.ID,
			Title:    row.Title,
			AppID:    row.AppID,
			Class:    row.Class,
			PID:      row.PID,
			X:        int(row.X),
			Y:        int(row.Y),
			W:        int(row.Width),
			H:        int(row.Height),
		}
		if id, err := strconv.ParseUint(info.NativeID, 0, 64); err == nil {
			info.ID = id
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// List returns windows reported by KWin scripting.
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
			return "\nvar listFunc = (typeof workspace.windowList === \"function\") ? workspace.windowList : workspace.clientList;\nvar wins = listFunc();\nvar rows = [];\nfor (var i = 0; i < wins.length; i++) {\n    var w = wins[i];\n    if (w.normalWindow) {\n        var g = w.frameGeometry;\n        var id = (typeof w.internalId !== 'undefined') ? w.internalId : w.windowId;\n        rows.push({id: String(id), title: String(w.caption || ''), app_id: String(w.resourceName || ''),\n            class: String(w.resourceClass || ''), pid: Number(w.pid) || 0, x: Number(g.x), y: Number(g.y),\n            width: Number(g.width), height: Number(g.height)});\n    }\n}\ncallDBus('" + svc + "', '/', '" + svc + "', 'ReportWindows', JSON.stringify(rows));\n"
		})
		if err != nil {
			yield(Info{}, err)
			return
		}

		infos, err := parseKWinWindowList(data)
		if err != nil {
			yield(Info{}, err)
			return
		}
		for _, info := range infos {
			if !yield(info, nil) {
				return
			}
		}
	}
}

const kwinScriptErrorPrefix = "__pf_error__:"

// ActiveTitle returns the caption of the currently focused window.
func (k *KWinScriptManager) ActiveTitle(ctx context.Context) (string, error) {
	return k.runScript(ctx, func(svc string) string {
		return "\nvar w = (typeof workspace.activeWindow !== 'undefined') ? workspace.activeWindow : workspace.activeClient;\ncallDBus('" + svc + "', '/', '" + svc + "', 'ReportWindows', w ? w.caption : '');\n"
	})
}

// Close closes the private D-Bus connection owned by the manager.
func (k *KWinScriptManager) Close() error {
	if k == nil || k.conn == nil {
		return nil
	}
	return k.conn.Close()
}

// Sync verifies that the KWin scripting connection is usable.
func (k *KWinScriptManager) Sync(ctx context.Context) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/kwinscript: sync canceled: %w", err)
	}
	return nil
}

// SupportedOperations returns operations supported by KWin scripting.
func (k *KWinScriptManager) SupportedOperations() []string {
	return []string{
		"discover",
		"info",
		"active-title",
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

// ActivateByID focuses the window identified by id.
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

// MoveByID positions the window identified by id.
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
	return "var g = w.frameGeometry;\n            w.frameGeometry = {x: " + strconv.Itoa(x) + ", y: " + strconv.Itoa(y) + ", width: Math.round(g.width), height: Math.round(g.height)};"
}

// ResizeByID resizes the window identified by id.
func (k *KWinScriptManager) ResizeByID(ctx context.Context, id string, width, height int) error {
	result, err := k.runScript(ctx, func(svc string) string {
		action := "var g = w.frameGeometry;\n            w.frameGeometry = {x: Math.round(g.x), y: Math.round(g.y), width: " + strconv.Itoa(width) + ", height: " + strconv.Itoa(height) + "};"
		return kwinFindByIDScript(id, svc, action)
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// CloseWindowByID closes the window identified by id.
func (k *KWinScriptManager) CloseWindowByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.closeWindow();")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// MinimizeByID minimizes the window identified by id.
func (k *KWinScriptManager) MinimizeByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.minimized = true;")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// MaximizeByID maximizes the window identified by id.
func (k *KWinScriptManager) MaximizeByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.setMaximize(true, true);")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// FullscreenByID reports that KWin scripting does not expose this operation.
func (k *KWinScriptManager) FullscreenByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

// UnfullscreenByID reports that KWin scripting does not expose this operation.
func (k *KWinScriptManager) UnfullscreenByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

// RestoreByID restores the window identified by id.
func (k *KWinScriptManager) RestoreByID(ctx context.Context, id string) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, "w.setMaximize(false, false); w.minimized = false;")
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// InfoByID returns fresh information for the window identified by id.
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
