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

Look at `pf info` first: capabilities depend on compositor support and user
permissions.

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

## Session support matrix

| Session | Flatpak expectation | Test coverage |
| --- | --- | --- |
| X11 / Xvfb | Screen, input, and window operations where X11 permits them | Real installed-bundle desktop smoke |
| KDE Wayland | Compositor / D-Bus window operations depend on KWin policy; portal input needs explicit user consent | Headless KWin probe and capability/error contract; consent requires attended test |
| GNOME Wayland | Native bridge must already be installed on the **host**; portal authorization is explicit | Headless Mutter probe and capability/error contract; bridge requires attended host test |
| Other Wayland (Weston / Sway) | Compositor-dependent; inspect capabilities | Backend-specific tests only |
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
