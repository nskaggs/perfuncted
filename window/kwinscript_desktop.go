//go:build linux

package window

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func parseKWinDesktops(data string) ([]DesktopInfo, error) {
	var desktops []DesktopInfo
	if err := json.Unmarshal([]byte(data), &desktops); err != nil {
		return nil, fmt.Errorf("window/kwinscript: parse desktops: %w", err)
	}
	if desktops == nil {
		return nil, fmt.Errorf("window/kwinscript: desktop response must be a JSON array")
	}
	// A JSON null current flag decodes into false, possibly fabricating
	// an unambiguous current desktop when the compositor actually supplied
	// an invalid state. Missing flags remain compatible with old providers.
	var rawDesktops []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &rawDesktops); err != nil {
		return nil, fmt.Errorf("window/kwinscript: parse desktop state: %w", err)
	}
	for i, row := range rawDesktops {
		if string(row["current"]) == "null" {
			return nil, fmt.Errorf("window/kwinscript: desktop %d has null current state", i)
		}
	}
	seen := map[string]bool{}
	seenPositions := map[int]bool{}
	currentCount := 0
	for index, d := range desktops {
		if d.ID == "" || seen[d.ID] || d.Position != index+1 || seenPositions[d.Position] {
			return nil, fmt.Errorf("window/kwinscript: invalid or repeated desktop id/position")
		}
		seen[d.ID] = true
		seenPositions[d.Position] = true
		if d.Current {
			currentCount++
		}
	}
	if len(desktops) > 0 && currentCount != 1 {
		return nil, fmt.Errorf("window/kwinscript: expected one current virtual desktop, got %d", currentCount)
	}
	return desktops, nil
}

// ListDesktops reports KWin virtual desktops in their current order.
func (k *KWinScriptManager) ListDesktops(ctx context.Context) ([]DesktopInfo, error) {
	data, err := k.runScript(ctx, func(svc string) string {
		return fmt.Sprintf(`
var ds=workspace.desktops || [];
var active=workspace.currentDesktop;
var activeId=active ? String(active.id) : null;
var items=[];
for(var i=0;i<ds.length;i++){
    var d=ds[i];
    items.push({id:String(d.id),name:String(d.name || ''),position:i+1,current:String(d.id)===activeId});
}
callDBus(%q,'/',%q,'ReportWindows',JSON.stringify(items));
`, svc, svc)
	})
	if err != nil {
		return nil, err
	}
	return parseKWinDesktops(data)
}

// SwitchDesktop selects an existing KWin virtual desktop by stable ID.
func (k *KWinScriptManager) SwitchDesktop(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("window/kwinscript: empty desktop ID: %w", ErrInvalidArgument)
	}
	result, err := k.runScript(ctx, func(svc string) string {
		return fmt.Sprintf(`
var ds=workspace.desktops || [];
var targetId=%s;
var ok=false;
var failure='';
try{
    for(var i=0;i<ds.length;i++){
        if(String(ds[i].id)===targetId){
            workspace.currentDesktop=ds[i];
            ok=true;
            break;
        }
    }
}catch(e){failure=String(e)}
callDBus(%q,'/',%q,'ReportWindows',failure ? %q+failure : (ok ? %q : %q));
`, strconv.Quote(id), svc, svc, kwinScriptErrorPrefix, kwinScriptFoundResult, "__pf_desktop_missing__")
	})
	if err != nil {
		return err
	}
	if result == "__pf_desktop_missing__" {
		return ErrDesktopNotFound
	}
	return kwinActionResultByID(id, result)
}

// SetWindowDesktopByID places a window on exactly one existing virtual desktop.
func (k *KWinScriptManager) SetWindowDesktopByID(ctx context.Context, windowID, desktopID string) error {
	if strings.TrimSpace(windowID) == "" || strings.TrimSpace(desktopID) == "" {
		return fmt.Errorf("window/kwinscript: window and desktop IDs must be nonempty: %w", ErrInvalidArgument)
	}
	js := fmt.Sprintf(`
var ds=workspace.desktops || [];
var dest=null;
for(var i=0;i<ds.length;i++){
    if(String(ds[i].id)===%s){dest=ds[i];break}
}
if(!dest){throw new Error('virtual desktop not found')}
w.desktops=[dest];
`, strconv.Quote(desktopID))
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(windowID, svc, js)
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(windowID, result)
}

// SetKeepAboveByID sets the KWin keep-above state for one exact window.
func (k *KWinScriptManager) SetKeepAboveByID(ctx context.Context, id string, on bool) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, fmt.Sprintf("w.keepAbove = %t;", on))
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// SetKeepBelowByID sets the KWin keep-below state for one exact window.
func (k *KWinScriptManager) SetKeepBelowByID(ctx context.Context, id string, on bool) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, fmt.Sprintf("w.keepBelow = %t;", on))
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// CursorPosition obtains the compositor's actual cursor position, which
// uinput and libei cannot read back from their injection device.
func (k *KWinScriptManager) CursorPosition(ctx context.Context) (int, int, error) {
	data, err := k.runScript(ctx, func(svc string) string {
		return fmt.Sprintf(`
var p=workspace.cursorPos;
callDBus(%q,'/',%q,'ReportWindows',JSON.stringify({x:Number(p.x),y:Number(p.y)}));
`, svc, svc)
	})
	if err != nil {
		return 0, 0, err
	}
	var value struct{ X, Y *float64 }
	if decodeErr := json.Unmarshal([]byte(data), &value); decodeErr != nil {
		return 0, 0, fmt.Errorf("window/kwinscript: cursor position: %w", decodeErr)
	}
	if value.X == nil || value.Y == nil {
		return 0, 0, fmt.Errorf("window/kwinscript: cursor position requires numeric x and y")
	}
	if math.IsNaN(*value.X) || math.IsNaN(*value.Y) {
		return 0, 0, fmt.Errorf("window/kwinscript: invalid cursor coordinates")
	}
	x, err := kwinGeometryInt(*value.X, "cursor x")
	if err != nil {
		return 0, 0, err
	}
	y, err := kwinGeometryInt(*value.Y, "cursor y")
	if err != nil {
		return 0, 0, err
	}
	return x, y, nil
}

// SetShadeByID requests real KWin titlebar shading. A compositor that
// disallows shading for this window returns a clear capability error.
func (k *KWinScriptManager) SetShadeByID(ctx context.Context, id string, on bool) error {
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, fmt.Sprintf(
			"if (!w.shadeable) { throw new Error('window is not shadeable in this compositor'); } w.shade = %t;", on))
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// TileWindowByID places a single window into the compositor's existing custom
// tiling layout at a global logical point. It never creates a fake tile by
// resizing a floating window. If KWin has no concrete tile there, fail.
func (k *KWinScriptManager) TileWindowByID(ctx context.Context, id string, x, y int) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("window/kwinscript: empty window ID: %w", ErrInvalidArgument)
	}
	action := fmt.Sprintf(`
if (!w.output || typeof workspace.tilingForScreen !== 'function') {
    throw new Error('KWin does not expose native tiling for this output');
}
var mgr = workspace.tilingForScreen(w.output);
if (!mgr || typeof mgr.bestTileForPosition !== 'function') {
    throw new Error('KWin tiling manager unavailable');
}
var chosen = mgr.bestTileForPosition(%d, %d);
if (!chosen || chosen.isLayout) {
    throw new Error('no concrete KWin tile at requested position');
}
w.tile = chosen;
if (!w.tile) {
    throw new Error('KWin did not associate window with requested tile');
}
`, x, y)
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, action)
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}

// FloatWindowByID clears the compositor-owned tile assignment, restoring the
// window to floating placement. This is not a substitute for a third-party
// tiling script's own state or rules.
func (k *KWinScriptManager) FloatWindowByID(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("window/kwinscript: empty window ID: %w", ErrInvalidArgument)
	}
	result, err := k.runScript(ctx, func(svc string) string {
		return kwinFindByIDScript(id, svc, `w.tile = null;
if (w.tile) { throw new Error('KWin did not release the tile'); }`)
	})
	if err != nil {
		return err
	}
	return kwinActionResultByID(id, result)
}
