## pf input

Mouse and keyboard injection

### Options

```
  -h, --help   help for input
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

* [pf](pf.md)	 - perfuncted — screen automation CLI
* [pf input click](pf_input_click.md)	 - Click a mouse button at coordinates
* [pf input click-center](pf_input_click-center.md)	 - Click the center of a rectangle
* [pf input click-screen](pf_input_click-screen.md)	 - Click at physical screenshot pixels via authoritative output and backend coordinates
* [pf input click-window](pf_input_click-window.md)	 - Click a window-relative logical point using an explicitly declared backend coordinate space
* [pf input coordinate-space](pf_input_coordinate-space.md)	 - Print the pointer backend coordinate space
* [pf input double-click](pf_input_double-click.md)	 - Double-click at coordinates
* [pf input drag-and-drop](pf_input_drag-and-drop.md)	 - Drag from one coordinate to another (press, move, release)
* [pf input keydown](pf_input_keydown.md)	 - Press and hold a key
* [pf input keyup](pf_input_keyup.md)	 - Release a held key
* [pf input location](pf_input_location.md)	 - Print current pointer location
* [pf input mousedown](pf_input_mousedown.md)	 - Press a mouse button (optional coords)
* [pf input mouseup](pf_input_mouseup.md)	 - Release a mouse button (optional coords)
* [pf input move](pf_input_move.md)	 - Move mouse to absolute coordinates
* [pf input scroll](pf_input_scroll.md)	 - Scroll the mouse wheel
* [pf input select-file](pf_input_select-file.md)	 - Select a local file in an existing native file picker, preserving clipboard
* [pf input sync](pf_input_sync.md)	 - Synchronize the input backend with the compositor
* [pf input type](pf_input_type.md)	 - Type a string or send keys (e.g. {enter}, {ctrl+s})
* [pf input type-literal](pf_input_type-literal.md)	 - Type text literally without interpreting key syntax

