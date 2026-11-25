# Screen capture and visual search

## Capture and inspect your desktop

```bash
# Capture a screenshot
pf screen grab --out - > screenshot.png

# Capture a selected window's visible screen rectangle by stable ID, exact title, or unique PID
pf screen grab --window-id 123 --out window.png --metadata window.json

# Capture that X11 window's own Composite pixmap without activating it
pf screen grab --window-id 123 --background --out window.png --metadata window.json

# List applications with accessibility support
pf a11y apps

# Check available desktop capabilities
pf info
```

Window-targeted capture uses window-manager-reported bounds and returns the
visible desktop composite for that rectangle. Overlapping windows may appear;
minimized or clipped targets and backends without usable geometry are rejected.
The optional JSON sidecar records the stable window ID, desktop rectangle,
image pixel bounds and scale, capture mode, geometry source, and occlusion
semantics. The `--window-title` selector matches the complete title
case-insensitively; `--pid` fails if the process owns more than one window.
With `--out -`, PNG bytes remain alone on stdout and the bounds report is
written to stderr.

`--background` requests a target-only buffer from an already redirected,
mapped X11 window. It never activates or redirects a window and never falls
back to the visible screen crop. The metadata state is `foreground`,
`background-visible`, `occluded`, or `unknown`; occlusion is conservatively
estimated from mapped top-level window bounding boxes, so shaped or
translucent windows can be reported as occluding. Minimized or unmapped
targets, X11 windows without an existing Composite pixmap, and non-X11 screen
backends return unsupported. The captured window buffer may be stale; its
freshness is not guaranteed. Current XTest/EIS input remains global and is not
treated as background-targeted input. The in-process API is
`ScreenBundle.CaptureWindowBackground`. Accessible controls can still be
invoked through the separately scoped `pf a11y action` operation when the
accessibility provider exposes the control; semantic actions are not raw
keyboard or mouse delivery.


See the [visual search commands](../docs-cli/pf_find.md) for pixel, template and OCR search.
