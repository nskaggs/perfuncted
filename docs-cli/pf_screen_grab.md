## pf screen grab

Capture a screen region or selected window as PNG

### Synopsis

Capture a screen region, or select one window with exactly one of --window-id,
--window-title, or --pid. Window title matching is exact and case-insensitive;
PID selection fails if that process owns multiple windows. Window selectors
cannot be combined with --rect. Window capture uses the window manager's
reported bounds to crop the visible desktop composite, so overlapping windows
may appear. Minimized, clipped, or geometry-unknown targets are rejected.

With a window selector, --background requests the target's own X11 Composite
pixmap. It works only when an X11 compositor has already redirected the
mapped target; it never activates or redirects a window and never falls back
to a screen crop. Visibility state is reported separately. Other backends,
unmapped targets, and minimized targets return an explicit unsupported error.

Use --metadata FILE to write the window ID, screen rectangle, image pixel
bounds and scale, capture mode, geometry source, and occlusion note as JSON.
With --out -, PNG bytes stay on stdout and the human-readable bounds report
goes to stderr.

```
pf screen grab [flags]
```

### Options

```
      --background            capture the target's own X11 window pixmap without activating it
  -h, --help                  help for grab
      --metadata string       write window-capture provenance as JSON to a separate file
      --out string            PNG output path or '-' for stdout (Flatpak defaults to stdout when redirected; native defaults to /tmp/pf-grab.png)
      --pid int32             capture the visible screen rectangle of one unique window owned by this process ID
      --rect string           x0,y0,x1,y1 (default "0,0,1920,1080")
      --window-id string      capture the visible screen rectangle of one stable window ID
      --window-title string   capture the visible screen rectangle of one exact case-insensitive window title
```

### Options inherited from parent commands

```
      --input-backend string        input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested                      start and target a new nested Wayland session
      --remember-input-permission   request persistent authorization for remote-desktop input
      --sync                        sync after observable mutating commands when supported
      --trace-actions               print each API action to stderr as it runs
      --trace-delay duration        sleep after each traced action
```

### SEE ALSO

* [pf screen](pf_screen.md)	 - Screen capture operations

