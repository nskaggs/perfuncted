# Release acceptance coverage

Run the same mandatory display-specific gate as GitHub Actions:
`just test-release-matrix headless-x11`, `nested-x11`, `headless-wayland`,
or `nested-wayland`. Visual and editor scenarios use fresh desktops so a
transient application/seat state cannot be mistaken for a pixel or input
regression. The regular integration suite remains a separate gate.

| Release surface | Independent evidence | Configuration |
|---|---|---|
| Go scripting and window-scoped steps | Real editor document persisted by a separately built CLI workflow | All four |
| Controlled scripts, completion and ID reuse | Terminal success and failure receipts, no post-failure output, file contents, decoded PNG/hash | All four |
| Clipboard | Native xclip/wl-copy/wl-paste both directions, whitespace, multiline and large Unicode | All four |
| Fuzzy template matching | Independent template positions; Go and CLI best/all/limited ordered results | All four |
| OCR | Real Tesseract Go and CLI recognition, matching desktop and pixel geometry, successful no-match | All four |
| Screen streaming | X11 independently painted exact dirty crop; Wayland pixel reconstruction across compositor redraw/resync; synthetic geometry/overflow reconstruction | All four plus unit |
| Timed movement/drag | Native Wayland client event stream, Go and CLI, multiple points, final coordinates, cancellation release; X11 rejects unavailable coordinate provenance without dispatch | All four |
| Pointer observation | Independent X11 QueryPointer readback, CLI provenance; explicit unsupported Wayland JSON | Both X11 modes |
| Global input observation and state | Strict native Xvfb keyboard/button observation; CLI button state from independent XTest; unsupported Wayland queries | Dedicated X11 |
| Window capture | Real window geometry and identity; CLI by ID and title with PNG and metadata; no unsupported output; redirected pixmap under occluder | All four / dedicated X11 |
| Named sessions | Independent real Sway owners, attachment, distinct native clipboard contents, duplicate/active recovery rejection, both owners stopped | Dedicated headless Wayland |
| Accessibility | Real Qt/GTK editor state waits, scoped absence, opposite-state rejection, verified mutations and disk save | Dedicated strict AT-SPI |
| Workflow control | Real headless Sway pause/resume/stop; process-protocol and stale-owner tests | Dedicated Wayland and unit |
| Release packaging | Static executable, GoReleaser live smoke, Flatpak build/install | CI |

This is a scenario inventory, not proof that every exported method, argument
combination, or Linux desktop is exercised live. GNOME/KWin native input,
portal/EIS permissions, uinput and hardware-specific configurations still
require their own appropriate environments; deterministic provider contracts
and explicit unsupported paths supplement, not replace, live certification.

Run repeated acceptance passes as separate test processes (each with
-count=1), not multiple repetitions in a single TestMain-managed compositor.
A shared Wayland session exhibited pointer-focus and subsequent wallpaper
fixture interference; fresh desktops keep independent scenarios strict.
The behavior of multiple unrelated clients sharing one desktop remains an
explicit investigation item rather than an asserted passing scenario.

The native Wayland event oracle exposed an intermittent final-motion loss when
a short-lived CLI closed its virtual-input connection immediately after a drag.
The regression requires the final destination and release, without accepting
a preceding interpolation sample. A bounded Wayland protocol acknowledgement
now precedes release and successful return; it is not application-level
delivery verification. Dedicated wire and ordering tests cover acknowledgement
failure and cancellation.

## Command inventory

The tracked ledger is at
[cmd/pf/testdata/release-command-coverage.tsv](cmd/pf/testdata/release-command-coverage.tsv).
The unit gate enumerates the actual runnable Cobra command tree and rejects
missing or stale entries. Entries marked unmapped have not been traced to a
named acceptance scenario; this does not assert that they lack unit tests.
Mapped entries identify a scenario and its applicable environment, rather than
claiming success on every backend. The ledger is a reviewed inventory, not
automatically measured per-command execution coverage.

The visual lane additionally checks independently calculated CRC32 hashes,
raw RGBA byte counts and colors, ordered and repeated pixel queries, PNG
contents, color tolerance, stable waits, scan results, and bounded unchanged
frame timeouts. The editor lane checks literal brace text on disk, window
discovery, active identity, geometry, and JSON watch removal after a verified
close. Native X11 checks separate-process key/button state and exact click and
scroll transitions. The AT-SPI certification retries invalidated read-only
observations without replaying mutations, requiring the original identity
and exact content.

### Mandatory command acceptance gate

The command inventory now rejects unmapped entries, unknown evidence tiers,
and stale test references. Run \`just test-release-command-contracts\` (also
part of \`just quality\` and the reusable GitHub quality workflow). It executes
every ledger-listed deterministic contract test and requires explicit pass
events, including absence of skipped subtests. The strict display matrix,
named session and AT-SPI lanes likewise require their named tests to pass,
not merely a zero exit status with no matching tests.

The native KDE acceptance script is mandatory in Flatpak and tagged release
workflows. Command contracts supplement actual desktop scenarios when the
available fixture cannot exercise the supported operation. In particular
shell completion, file-dialog selection, some coordinate/window mutations,
and accessibility point/event commands have deterministic contract evidence;
this is not a claim that every command has a successful live run on every
desktop or that every flag/backend combination has been certified.
