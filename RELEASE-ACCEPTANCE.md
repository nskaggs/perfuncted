# Release acceptance coverage

Run the same mandatory display-specific gate as GitHub Actions:
`just test-release-matrix headless-x11`, `nested-x11`, `headless-wayland`,
or `nested-wayland`. Visual and editor scenarios use fresh desktops so a
transient application/seat state cannot be mistaken for a pixel or input
regression. The regular integration suite remains a separate gate.

| Release surface | Independent evidence | Configuration |
|---|---|---|
| Go scripting and window-scoped steps | Real editor document persisted by a separately built CLI workflow | All four |
| Controlled scripts, completion and ID reuse | Terminal status plus file contents and decoded PNG/hash | All four |
| Clipboard | Native xclip/wl-copy/wl-paste both directions, whitespace, multiline and large Unicode | All four |
| Fuzzy template matching | Independently rendered template positions and ordering | All four |
| OCR | Real Tesseract recognition of independently rendered glyphs | All four |
| Screen streaming | X11 independently painted exact dirty crop; Wayland pixel reconstruction across compositor redraw/resync; synthetic geometry/overflow reconstruction | All four plus unit |
| Timed movement/drag | Native Wayland client event stream, Go and CLI, multiple points, final coordinates, cancellation release; X11 rejects unavailable coordinate provenance without dispatch | All four |
| Pointer observation | Independent X11 QueryPointer readback | Both X11 modes |
| Global input observation and state | Strict native Xvfb keyboard/button observation, focus, shutdown and cancellation | Dedicated X11 |
| Window capture | Real window geometry and identity; separately redirected pixmap under occluder | All four / dedicated X11 |
| Named sessions | Independent real Sway owners, attachment, duplicate/active recovery rejection, isolation, stop | Dedicated headless Wayland |
| Accessibility | Real Qt/GTK editor state waits, scoped absence, opposite-state rejection, verified mutations and disk save | Dedicated strict AT-SPI |
| Workflow control | Real headless Sway pause/resume/stop; process-protocol and stale-owner tests | Dedicated Wayland and unit |
| Release packaging | Static executable, GoReleaser live smoke, Flatpak build/install | CI |

This is a scenario inventory, not proof that every exported method, argument
combination, or Linux desktop is exercised live. GNOME/KWin native input,
portal/EIS permissions, uinput and hardware-specific configurations still
require their own appropriate environments; deterministic provider contracts
and explicit unsupported paths supplement, not replace, live certification.
