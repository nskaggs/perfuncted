# Desktop support

Perfuncted supports X11 and several Wayland environments, including GNOME,
KDE Plasma, and Sway. Available operations depend on the desktop and its
permissions. Run `pf info` to see which capabilities are available.

**GNOME Shell bridge:** Perfuncted includes a versioned GNOME Shell integration
for native window control, input, screenshots, and clipboard access. The native
CLI installs the bridge when a GNOME capability requires it. GNOME Shell may
require one logout and login to load it. The bundled bridge declares support
for GNOME Shell 46 through 51. Flatpak can use an existing host installation
of the bridge, but cannot install the host extension from inside its sandbox.

Wayland desktop portals may request user authorization for capture or input.
See [Flatpak setup and permissions](../FLATPAK.md) for supported paths and
troubleshooting.

## Selection and permissions

`pf info` reports its selected backend. On KDE Wayland, Perfuncted tries compositor virtual input, then `/dev/uinput` configured from validated logical output geometry, and finally XTest when the compositor-wide options are unavailable. **XTest is limited to X11/XWayland windows**, and `pf info` warns about it; successful XTest initialization is not evidence that native Wayland windows can be controlled. Pure X11 still prefers XTest. Forced uinput (`PF_FORCE_INPUT=uinput`) never silently switches backends.

For full Wayland desktop input, opt in to a portal-authorized session using `pf --input-backend remote-desktop ...`. This requests normal user consent; `--remember-input-permission` explicitly requests persistent permission. There is no implicit portal consent or hidden credential extraction. The remote-desktop flag must be permitted on `pf info` as well as commands that require input.
