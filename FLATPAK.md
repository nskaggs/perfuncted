# Flatpak CLI: install and automate your first Linux desktop

Flatpak distributes the `pf` command for the **existing desktop**. It does not
provide an unrestricted host shell or automatically install a nested compositor.

## Install

Download `perfuncted.flatpak` **and** `pf-flatpak` from the same GitHub
release into one directory. A repository checkout is not required.

```sh
flatpak install --user -y ./perfuncted.flatpak
install -Dm755 ./pf-flatpak "$HOME/.local/bin/pf"
export PATH="$HOME/.local/bin:$PATH"  # add to your shell profile
pf --help
pf version
pf session check
pf info --output=json
```

Without the launcher, use `flatpak run io.github.nskaggs.perfuncted`
followed by normal `pf` arguments. Flatpak itself does **not** create a host
`pf` executable, even though the AppStream metadata identifies that binary.

## First automation

```sh
pf info
pf window list
pf screen resolution
pf screen grab --rect 0,0,640,480 > screenshot.png
```

The PNG is written to the **host** via standard output, not to Flatpak's
private temporary directory. When the installed Flatpak's `pf screen grab`
has no `--out`, it streams a PNG if stdout is redirected; it rejects
interactive terminal output rather than dumping binary bytes into your shell.
`--out -` always streams. For a file path, use an explicitly shared directory.

Look at `pf info` first: capabilities depend on
compositor support and user permissions. An unavailable input backend is an
honest limitation, not a failed package installation. Use `pf session check`
for setup diagnostics.

## Host files: explicit and narrow

By default the Flatpak has no permission to read arbitrary home-directory files
or to write into arbitrary host paths; its `/tmp` is private. Prefer pipes:

```sh
pf screen grab-region --rect 0,0,640,480 --out - > screenshot.png
cat task.pf | pf run -
```

For path-based operations, the optional host launcher supports specific
directories. These launcher flags must come **before** the regular CLI command:

```sh
pf --host-dir-ro "$PWD" run "$PWD/task.pf"
pf --host-dir "$PWD" screen grab --out "$PWD/screenshot.png"
```

The `--host-dir` and `--host-dir-ro` flags are **not** native `pf` flags.
You can also invoke Flatpak directly with `flatpak run --filesystem="$PWD"`
(or `--filesystem="$PWD":ro` for read-only access).

## Upgrade from a previous Flatpak release

To replace a previously downloaded Flatpak with a newer downloaded bundle:

```sh
flatpak install --user -y --reinstall ./perfuncted.flatpak
pf version
```

This preserves application data and user-specific Flatpak overrides. CI now
tests this path against the actual published v1.2.3 bundle (with a pinned
SHA-256 hash), including a read-only directory grant and a separate directory
that must remain inaccessible. The old v1.2.3 bundle did not stamp its
version metadata, so identification comes from its pinned release checksum.

## Session support matrix

| Session | Flatpak expectation | Test coverage |
| --- | --- | --- |
| X11 / Xvfb | Screen, input, and window operations where X11 permits them | Real installed-bundle desktop smoke |
| KDE Wayland | Compositor / D-Bus window operations depend on KWin policy; portal input needs explicit user consent | Headless KWin probe and capability/error contract; consent requires attended test |
| GNOME Wayland | Native bridge must already be installed on the **host**; portal authorization is explicit | Headless Mutter probe and capability/error contract; bridge requires attended host test |
| Other Wayland (Weston / Sway) | Sandboxed compositor sockets may filter privileged virtual input; use an authorized portal if supported and do not assume native wlroots input is available inside Flatpak | Real installed Flatpak capability/fail-closed verification plus native unsandboxed backend tests |
| Isolated `pf session start` | Not bundled as a complete nested desktop in Flatpak | Prefer native CLI |

Desktop controls blocked by the compositor must fail with a diagnostic, **not**
silently fall back to an unauthorized input backend.

If supported, `pf --input-backend remote-desktop input click --x 100 --y 100`
starts an **interactive authorization** through the XDG RemoteDesktop portal.
Permission restoration is always opt-in, using `--remember-input-permission`.

## Troubleshooting

- **`pf: command not found`**: install `scripts/pf-flatpak` to
  `~/.local/bin/pf` and add `~/.local/bin` to your PATH.
- **No desktop capability**: run `pf session check` and `pf info`; inspect
  compositor, portal availability and session sockets. A Wayland session may
  only expose XWayland X11 controls.
- **Screenshot not on host**: use `grab-region --out - > file.png` or grant
  the target directory to the Flatpak.
- **Cannot read script**: use `pf run -` with stdin, or
  `pf --host-dir-ro "$PWD" run "$PWD/file.pf"`.
- **GNOME bridge missing**: install the native host bridge first, then
  logout/login. A Flatpak cannot automatically install GNOME Shell extensions.
- **Native Sway/wlroots input unavailable in Flatpak**: the Flatpak Wayland
  socket can filter privileged virtual keyboard/pointer protocols even when
  the same protocol works from the native host executable. `pf info`
  must report this as unavailable. Do not add `--device=all` or bypass the
  compositor's input permissions. Where supported, explicitly request
  `--input-backend remote-desktop` and authorize it through the desktop portal.
- **Portal denied input**: request `--input-backend remote-desktop` on a
  user-visible supported desktop and answer the compositor's consent prompt.
- **Nested session tools missing**: `sway`, `dbus-daemon`, and `wl-paste`
  must be installed separately; use the native binary for full headless mode.

The Flatpak intentionally omits broad `--device=all` access. Direct
`/dev/uinput` is not automatically accessible: this is a sandbox
restriction, not a failed permission restoration.

## Real-desktop acceptance (attended)

Automated CI starts Xvfb and headless GNOME/KWin compositors and verifies the
installed bundle's behavior. A headless session cannot respond to a
RemoteDesktop consent dialog or automatically provision a GNOME Shell
extension on the user's account.

To validate **native Wayland input**, use a disposable local desktop with an
ordinary visible GUI application, run `pf info`, then invoke
`pf --input-backend remote-desktop input click --x ... --y ...` and
`pf --input-backend remote-desktop input type ...`, explicitly approve the
desktop portal, and confirm the application actually receives both events.
Repeat after denying permission, canceling a pending request, revoking
permission, restarting the application, and (only if explicitly enabled)
restoring a saved permission. No unattended CI result is equivalent to this
consent-and-control test. GNOME additionally needs the native host bridge
installed to validate its bridge-specific window operations.

For an actual attended verification, download the optional
`pf-flatpak-attended-check` asset from the same release and run:

```sh
chmod +x ./pf-flatpak-attended-check
./pf-flatpak-attended-check allow
./pf-flatpak-attended-check deny
./pf-flatpak-attended-check cancel
./pf-flatpak-attended-check restore
./pf-flatpak-attended-check revoke
./pf-flatpak-attended-check bridge  # GNOME only, requires installed host bridge
./pf-flatpak-attended-check capture # Approve screenshot authorization; verify disposable PNG
./pf-flatpak-attended-check capture-deny   # Deny screenshot permission; require no pixels
./pf-flatpak-attended-check capture-cancel # Dismiss screenshot permission; require no pixels
```

The `capture` mode requests a real 64×48 screenshot, allows the user to respond to any desktop consent dialog, checks the resulting PNG header and dimensions, then deletes the captured image. It is intentionally not an unattended CI check. The `capture-deny` and `capture-cancel` modes separately require the screenshot command to fail and produce no bytes; a person must also confirm that the intended portal dialog was actually shown and denied or dismissed, rather than merely unavailable.

The input-validation modes open only a disposable editor document. In the positive cases it
asks for a visible editor text-area coordinate, sends a **portal-authorized
mouse click**, then types and saves a unique marker and verifies the saved
content. It waits for explicit human consent/denial; negative cases verify
input rejection without modifying that document. It will refuse
unattended execution. The `restore` case opts into persistent permission and
checks a second CLI process; the `revoke` case requires changing permissions
in the desktop settings. The `cancel` case rejects an abandoned permission
prompt. The GNOME-only `bridge` case requires the host extension already
installed, then checks window discovery and verified activation. These tests
use the **actual** installed Flatpak and native Wayland editor; run the portal
modes separately on GNOME and KDE.
