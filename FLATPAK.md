# Flatpak CLI: install and automate your first Linux desktop

Flatpak distributes the `pf` command for the **existing desktop**. It does not
provide an unrestricted host shell or automatically install a nested compositor.

## Install

Download the single `perfuncted.flatpak` bundle from the
[GitHub releases](https://github.com/nskaggs/perfuncted/releases).
Install and run it with Flatpak itself—no secondary wrapper or helper required:

```sh
flatpak install --user -y ./perfuncted.flatpak
flatpak run io.github.nskaggs.perfuncted --help
flatpak run io.github.nskaggs.perfuncted version
flatpak run io.github.nskaggs.perfuncted session check
flatpak run io.github.nskaggs.perfuncted info --output=json
```

Flatpak applications do not automatically export a short host-side `pf` command;
the supported invocation is `flatpak run io.github.nskaggs.perfuncted`. A
native (non-Flatpak) CLI is also available separately for developers who need
to run `pf` directly.

## First automation

```sh
flatpak run io.github.nskaggs.perfuncted info
flatpak run io.github.nskaggs.perfuncted window list
flatpak run io.github.nskaggs.perfuncted screen resolution
flatpak run io.github.nskaggs.perfuncted screen grab --rect 0,0,640,480 > screenshot.png
```

The screenshot is streamed into a **host** file through standard output. This
does not require write access to host folders. Without an explicit `--out`,
Flatpak streams PNG pixels when stdout is redirected and refuses to dump
binary pixels into an interactive terminal. A path specified with `--out` must
be accessible inside the sandbox.

Use `info` first: capabilities depend on compositor support, desktop portals
and permissions. Use `session check` for setup diagnostics.

## Host files and permissions

Flatpak runs inside a sandbox: it cannot ordinarily read arbitrary home files,
and its `/tmp` is private. For normal CLI workflows, **pipes avoid filesystem
permissions entirely**:

```sh
cat task.pf | flatpak run io.github.nskaggs.perfuncted run -
flatpak run io.github.nskaggs.perfuncted screen grab --rect 0,0,640,480 > screenshot.png
```

When a host path is needed, use Flatpak's official **per-invocation**
`--filesystem` option, placed before the application ID:

```sh
flatpak run --filesystem="$PWD":ro io.github.nskaggs.perfuncted run "$PWD/task.pf"
flatpak run --filesystem="$PWD" io.github.nskaggs.perfuncted screen grab --out "$PWD/screenshot.png"
```

The first command grants **read-only** access to the working directory. The
second permits writes to that directory for that invocation. Neither grant is
persisted, nor does it make any other folder available.

If a particular folder should remain accessible to subsequent invocations,
use a **user-scoped Flatpak override** instead:

```sh
flatpak override --user --filesystem="$HOME/Documents/Automation":ro io.github.nskaggs.perfuncted
flatpak override --user --show io.github.nskaggs.perfuncted
```

Inspect or revoke overrides using `flatpak override --user --show` or
`flatpak override --user --reset io.github.nskaggs.perfuncted`. Reset removes
**all** user overrides for this application, not just one folder. Flatpak
desktop portals are preferable where available; they request permission for
individual operations. There is no default `--filesystem=home`, `--device=all`,
or unrestricted host filesystem access.

See the [official Flatpak sandbox permission guide](https://docs.flatpak.org/en/latest/sandbox-permissions.html)
and [Flatpak command reference](https://docs.flatpak.org/en/latest/flatpak-command-reference.html).

## Upgrade from a previous Flatpak release

To replace a previously downloaded Flatpak with a newer downloaded bundle:

```sh
flatpak install --user -y --reinstall ./perfuncted.flatpak
flatpak run io.github.nskaggs.perfuncted version
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

If supported, `flatpak run io.github.nskaggs.perfuncted --input-backend remote-desktop input click --x 100 --y 100`
starts an **interactive authorization** through the XDG RemoteDesktop portal.
Permission restoration is always opt-in, using `--remember-input-permission`.

## Troubleshooting

- **Command unavailable:** use `flatpak run io.github.nskaggs.perfuncted`.
  Flatpak does not install a separate host `pf` executable.
- **Missing capability:** use `flatpak run io.github.nskaggs.perfuncted info`
  and `flatpak run io.github.nskaggs.perfuncted session check`; capabilities
  vary by compositor and its permission model.
- **Screenshot missing on host:** redirect PNG stdout to a host file, or
  pass a narrowly scoped `flatpak run --filesystem=DIR` grant with `--out`.
- **Cannot read a host script:** pipe its contents using `run -` or grant
  read-only access to its containing directory with `--filesystem=DIR:ro`.
- **GNOME bridge missing:** install the host-side native bridge first, then
  log out/in as needed. A Flatpak cannot automatically install Shell extensions.
- **Wayland input unavailable:** the sandbox may filter native pointer and
  keyboard injection even when the host executable can use it. Where supported,
  request `--input-backend remote-desktop` and explicitly authorize the desktop
  portal. Do not bypass input permissions or use unrestricted device grants.
- **Nested session tools missing:** use the native CLI for full headless mode;
  this Flatpak does not package a complete nested compositor.

## Real-desktop acceptance (attended)

Automated CI starts Xvfb and headless GNOME/KWin compositors and verifies the
installed bundle's behavior. A headless session cannot respond to a
RemoteDesktop consent dialog or automatically provision a GNOME Shell
extension on the user's account.

To validate **native Wayland input**, use a disposable local desktop with an
ordinary visible GUI application, run
`flatpak run io.github.nskaggs.perfuncted info`, then invoke
`flatpak run io.github.nskaggs.perfuncted --input-backend remote-desktop input click --x ... --y ...`
and `flatpak run io.github.nskaggs.perfuncted --input-backend remote-desktop input type ...`,
explicitly approve the
desktop portal, and confirm the application actually receives both events.
Repeat after denying permission, canceling a pending request, revoking
permission, restarting the application, and (only if explicitly enabled)
restoring a saved permission. No unattended CI result is equivalent to this
consent-and-control test. GNOME additionally needs the native host bridge
installed to validate its bridge-specific window operations.

The interactive permission-approval checks are not release downloads.
Project maintainers can use the repository-only
`scripts/pf-flatpak-attended-check` helper on a visible GNOME or KDE Wayland
desktop. It requires an operator to approve or deny desktop permission prompts;
unattended CI does not substitute for those interactions.
