Design: First-Class GNOME Integration

Summary

Provide a bundled GNOME Shell integration for the privileged desktop
capabilities that GNOME Wayland intentionally does not expose to ordinary
applications. Explicit GNOME Shell and generic compositor backends remain
available wherever they are the supported runtime capability.

The goal is not merely to fix window control.

The goal is:

On a supported stock GNOME Wayland desktop, perfuncted's complete public API works without unsafe mode, portal prompts, /dev/uinput permissions, or separately installed clipboard utilities.

Perfuncted exposes five desktop capabilities:

screen

input

windows

outputs

clipboard

Those capabilities already resolve independently, so this can be implemented without changing the public API or turning GNOME into a special case at the application layer.

GNOME's existing output support already works through ordinary Wayland and requires no additional privilege. The other four capabilities have significant GNOME-specific gaps.

The new architecture therefore adds one perfuncted GNOME integration that supplies:

windows

screen capture

keyboard/pointer input

clipboard

Existing Wayland output discovery remains in place unless later testing identifies a reason to replace it.

This is intentionally not called a "window extension." It is the GNOME backend for perfuncted.

Desired User Experience

After initial installation:

$ pf info

desktop: GNOME Wayland

screen       gnome-native    available
input        gnome-native    available
windows      gnome-native    available
outputs      wayland         available
clipboard    gnome-native    available

And:

$ pf window list
$ pf screenshot out.png
$ pf type "hello world"
$ pf click 400 300
$ pf clipboard get
$ pf clipboard set "hello"

all work without:

GNOME unsafe mode;

org.gnome.Shell.Eval;

portal dialogs;

wl-copy;

wl-paste;

xclip;

/dev/uinput;

membership in the input group;

XWayland;

user configuration of individual backends.

The only remaining unavoidable GNOME-specific setup boundary is extension activation itself: a freshly installed local GNOME Shell extension may not be loaded until the next GNOME session. A live first install can therefore require one logout/login. After that, ordinary use should require no GNOME-specific action.

Fundamental Architecture

                         perfuncted
                             |
       +---------------------+----------------------+
       |            |             |          |      |
     Screen        Input        Windows    Output Clipboard
       |            |             |          |      |
       +------------+-------------+          |      |
                    |                        |      |
                    v                        |      |
        GNOME integration D-Bus client <-----+------+
                    |
                    v
   io.github.nskaggs.perfuncted.Gnome1
                    |
                    v
      bundled GNOME Shell extension
                    |
       +------------+-------------+----------+
       |            |             |          |
 Shell.Screenshot Clutter      Meta.Window St.Clipboard
                virtual input

Outputs continue through the normal Wayland output backend because they already work without privilege or additional dependencies.

The extension exists solely because the other operations require compositor/Shell authority.

Goals

1. Complete GNOME capability coverage

On a supported GNOME Wayland desktop, perfuncted should provide the complete documented capability surface:

Windows

list/discover windows

session-stable window IDs

get focused window

title/application/PID/geometry/state

activate

move

resize

minimize

maximize

restore

fullscreen

unfullscreen

close

lifecycle events

focus events

Screen

full-screen capture

arbitrary region capture

normal pixel/hash operations through existing Go code

Input

literal text

named keys

key down/up

modifier combinations

Unicode text

absolute pointer movement

click

button down/up

vertical/horizontal scrolling

current pointer position

sync/flush semantics

Clipboard

get text

set text

Outputs

Continue using existing Wayland output discovery unless richer GNOME-specific behavior becomes necessary.

2. No unsafe mode

Normal GNOME operation must not require:

org.gnome.Shell.Eval

or GNOME Shell unsafe mode.

3. No normal-path portal prompts

Screen capture should happen inside the trusted Shell extension rather than through the desktop portal.

Portal capture may remain as a fallback when the integration is unavailable.

4. No normal-path uinput configuration

Input should be injected through Mutter/Clutter virtual input devices created inside GNOME Shell.

The user should not normally need:

/dev/uinput;

an input group;

a udev rule.

5. No GNOME clipboard helper dependency

Clipboard access should go directly through GNOME Shell's clipboard API rather than requiring wl-copy/wl-paste.

6. One perfuncted distribution

The GNOME integration must be:

stored in the perfuncted repository;

versioned with perfuncted;

bundled in perfuncted releases;

installed/upgraded by perfuncted;

invisible as a separate product.

Users should not need to discover or manage a companion extension themselves.

Non-Goals

This project does not attempt to:

create a general-purpose GNOME automation service;

expose arbitrary GNOME Shell internals;

add arbitrary JavaScript evaluation;

add arbitrary method/property access;

replace the existing cross-desktop public APIs;

move matching, waiting, retries, or orchestration into JavaScript;

support every historical GNOME Shell version;

bypass GNOME's extension lifecycle.

GNOME APIs Used

Windows

Use Mutter's Meta.Window and Meta.Display.

The extension should expose only the operations perfuncted requires.

Use Mutter's stable sequence as the native session-bound window ID:

String(window.get_stable_sequence())

These IDs are stable for the life of a window in the current GNOME Shell session. They are not persistent across logout/login, Shell restart, or reboot.

Screen

Use Shell.Screenshot from inside the extension.

The extension should capture:

entire desktop;

arbitrary rectangular regions.

Image matching, hashing, waiting, decoding, and other image operations remain in Go.

Input

Use the current Clutter/Mutter seat APIs to create virtual devices from inside GNOME Shell.

The extension should create:

one virtual keyboard;

one virtual pointer.

The narrow bridge should support the input primitives required by perfuncted.

Clipboard

Use GNOME Shell's St.Clipboard.

The current perfuncted clipboard API is text-only, so the bridge only needs text get/set initially.

Outputs

Keep current Wayland output enumeration initially.

Do not move outputs into the extension merely for architectural symmetry.

Extension Structure

Suggested repository layout:

gnome-extension/
    perfuncted@nskaggs.github.io/
        metadata.json
        extension.js
        service.js
        windows.js
        screen.js
        input.js
        clipboard.js

Characteristics:

one extension;

one UUID;

one D-Bus service;

no panel icon;

no preferences UI;

no persistent user state;

no separate updater;

no independent release process.

The extension should be small enough to audit as a privileged adapter.

D-Bus Service

Use one versioned service:

bus:
io.github.nskaggs.perfuncted.Gnome1

path:
/io/github/nskaggs/perfuncted/Gnome1

Expose capability-specific interfaces rather than one generic command endpoint.

Core

io.github.nskaggs.perfuncted.Gnome1.Core

Methods:

GetProtocolVersion() -> u
GetExtensionVersion() -> s
GetShellVersion() -> s
GetCapabilities() -> as
Ping()

Example capabilities:

[
    "windows",
    "screen",
    "input",
    "clipboard"
]

The protocol version is independent of the perfuncted release version.

Compatible protocol versions should continue working across ordinary perfuncted upgrades.

Window Interface

io.github.nskaggs.perfuncted.Gnome1.Windows

Methods:

ListWindows()
GetWindow(id)
GetActiveWindow()

Activate(id)
Move(id, x, y)
Resize(id, width, height)

Minimize(id)
Maximize(id)
Restore(id)

Fullscreen(id)
Unfullscreen(id)

Close(id)

Signals:

WindowAdded(window)
WindowRemoved(id)
WindowChanged(window)
FocusChanged(id)

The returned window representation should map directly onto window.Info:

id
title
app_id
class
pid

x
y
width
height

active
minimized
maximized
fullscreen

Use D-Bus-native structures/variants, not JSON strings.

Window enumeration should use the current Mutter window APIs rather than reconstructing the old global.get_window_actors() Eval implementation literally.

Preserve existing skip-taskbar semantics unless testing demonstrates a reason to change them.

Screen Interface

io.github.nskaggs.perfuncted.Gnome1.Screen

Do not expose arbitrary output file paths to privileged Shell code.

Preferred transport:

Go
  |
  | Unix FD over D-Bus
  v
GNOME extension
  |
  | Gio.OutputStream
  v
Shell.Screenshot

Conceptual API:

CaptureFull(fd)
CaptureRegion(fd, x, y, width, height)

The Go backend:

creates a memfd or anonymous temporary file;

passes the FD over D-Bus;

asks the extension to write PNG data into it;

rewinds it;

decodes it with the existing Go image path.

This avoids:

arbitrary privileged filesystem writes;

large screenshot payloads encoded as ordinary D-Bus byte arrays;

portal consent in the normal path.

If high-level GJS D-Bus helpers make FD passing awkward, use lower-level GIO D-Bus APIs for this interface rather than weakening the design.

Input Interface

io.github.nskaggs.perfuncted.Gnome1.Input

Create virtual devices during extension enable.

Conceptually:

const seat = global.backend.get_default_seat();

this._keyboard =
    seat.create_virtual_device(
        Clutter.InputDeviceType.KEYBOARD_DEVICE);

this._pointer =
    seat.create_virtual_device(
        Clutter.InputDeviceType.POINTER_DEVICE);

Exact API names should be feature-tested against supported GNOME releases.

Narrow methods

Conceptually:

Key(keyval, pressed)
Text(text)

PointerMove(x, y)
PointerButton(button, pressed)
Scroll(axis, amount)

PointerLocation() -> (x, y)

Sync()

A batched method should be considered for keyboard operations:

KeyEvents(events)

to avoid one D-Bus round trip per keystroke.

Keep parsing in Go

Perfuncted's existing input syntax stays in Go.

For example:

hello{ctrl+s}

Go remains responsible for turning that into:

literal text;

named keys;

modifiers;

key-down/key-up events.

The extension receives resolved input primitives only.

Do not move the input mini-language into JavaScript.

Unicode

Prefer compositor-native keyval/Unicode injection rather than trying to recreate XKB layout machinery inside the extension.

The implementation spike must prove Unicode typing in a native Wayland application, not merely an XWayland application.

Clipboard Interface

io.github.nskaggs.perfuncted.Gnome1.Clipboard

Methods:

GetText() -> s
SetText(text)

Back these directly with St.Clipboard.

This should completely satisfy the current text clipboard API.

wl-copy, wl-paste, and xclip remain generic fallbacks for non-GNOME environments.

Go Architecture

Add a shared low-level client:

internal/gnomebridge/
    client.go
    protocol.go
    install.go
    probe.go
    errors.go

It owns:

D-Bus constants;

connection lifecycle;

protocol negotiation;

common errors;

extension presence detection;

extension installation/update state;

capability discovery.

It must not implement perfuncted's public capability interfaces.

Each existing capability package gets a narrow adapter:

screen/gnome_native.go
input/gnome_native.go
window/gnome_native.go
clipboard/gnome_native.go

Examples:

type GnomeInputBackend struct {
    bridge *gnomebridge.Client
}

type GnomeScreenBackend struct {
    bridge *gnomebridge.Client
}

The existing package-level runtime selection remains intact:

screen.OpenRuntime()
input.OpenRuntime()
window.OpenRuntime()
clipboard.OpenRuntime()
output.OpenRuntime()

GNOME-specific knowledge should not leak into the root Session beyond ordinary backend selection.

Backend Priority

Screen

New GNOME priority:

perfuncted GNOME integration
    ↓
GNOME Shell screenshot backend
    ↓
portal

The bundled integration is preferred. The GNOME Shell screenshot backend and
portal are explicit current capability paths when the integration is
unavailable.

Input

New GNOME priority:

perfuncted GNOME integration
    ↓
generic Wayland/X11/uinput fallbacks

The native GNOME input backend should win whenever the bridge is active.

Windows

New GNOME priority:

perfuncted GNOME integration
    ↓
GNOME Shell Eval backend

The bundled integration is preferred; Shell Eval remains a supported backend
for GNOME sessions where the integration is unavailable.

Clipboard

New GNOME priority:

perfuncted GNOME integration
    ↓
wl-clipboard
    ↓
xclip

Outputs

Unchanged:

Wayland
    ↓
X11 fallback

Provisioning

Embed the extension into the Go distribution using go:embed.

Suggested internal asset path:

internal/gnomebridge/assets/
    perfuncted@nskaggs.github.io/
        metadata.json
        extension.js
        ...

The exact extension corresponding to the perfuncted release is therefore always available locally.

No network access should be required to provision it.

Automatic Installation

When perfuncted detects GNOME and a GNOME-native capability is requested:

bridge service running?
    |
   yes
    |
protocol compatible?
    |
   yes
    v
use it

If absent:

bundled extension present?
    |
   yes
    v
install/update extension
    |
    v
ensure enabled for next session

The normal user must not have to:

visit extensions.gnome.org;

search for the extension;

clone another repository;

manually copy extension files;

know the UUID.

First-Install Session Boundary

A newly installed local GNOME Shell extension is not necessarily loaded into the already-running Shell session.

Therefore define an explicit typed condition:

ErrGNOMESessionRestartRequired

Example CLI behavior:

$ pf window list

Perfuncted installed its GNOME integration.

Log out and back in once to activate it.
No additional GNOME setup will be required.

This is preferable to a vague unsupported-backend error.

The "just works" promise begins after this unavoidable GNOME lifecycle boundary.

Enabling

Installation should also ensure the extension is enabled for the next GNOME session.

Do not require the user to open GNOME Extensions manually.

Any code that modifies the enabled-extension setting must:

preserve all unrelated enabled extensions;

preserve explicit user-disabled state where applicable;

never replace the complete list with a hard-coded perfuncted-only value.

Use GNOME's own extension-management mechanisms where possible.

Upgrades

Separate protocol compatibility from release-version equality.

On startup:

running bridge compatible?
        |
       yes
        |
 bundled extension newer?
        |
       yes
        v
update files quietly
        |
        v
continue using running compatible bridge

The updated extension activates naturally at the next login.

Do not force a logout merely because the embedded files changed.

If the running bridge is protocol-incompatible:

install the matching bundled extension;

return ErrGNOMESessionRestartRequired;

use the new protocol after the next session begins.

GNOME Version Policy

Initially support:

GNOME Shell 45+

GNOME 45 is the clean modern-extension boundary because the extension ecosystem moved to ES modules.

Prefer feature detection for smaller API differences.

Avoid separate per-release extension implementations unless actual compatibility evidence makes them necessary.

Supported GNOME versions should be exercised by CI or explicit integration environments rather than claimed from metadata alone.

Security Model

The expanded extension is intentionally powerful.

It can:

inspect desktop windows;

control desktop windows;

capture the screen;

read clipboard text;

set clipboard text;

inject arbitrary keyboard input;

inject pointer input.

That is effectively full desktop-automation authority.

This is appropriate because it mirrors the job perfuncted already advertises.

However, the D-Bus surface must remain explicit.

Never expose:

Eval()
Execute()
RunCommand()
CallMethod()
GetProperty()
SetProperty()
ImportModule()

or any generic escape hatch.

The API should mirror perfuncted's documented capability surface and no more.

Sandboxed applications do not automatically gain access to an arbitrary session-bus name. The perfuncted Flatpak should explicitly receive access only to the perfuncted GNOME bridge.

Flatpak

Add:

--talk-name=io.github.nskaggs.perfuncted.Gnome1

Once all GNOME functionality goes through the bridge, evaluate whether the existing broad:

--talk-name=org.gnome.Shell

permission can be removed.

Do not remove it until every remaining perfuncted path that relies on it has been accounted for.

Host extension provisioning from inside Flatpak is a separate packaging concern and must be tested rather than assumed.

Complexity Rule

The extension is a privileged driver, not the application.

Anything that can live in Go should live in Go.

JavaScript owns only

Mutter window access;

Shell screenshot invocation;

Clutter virtual input devices;

direct clipboard access;

Shell/Mutter event signals.

Go owns

public API;

capability abstraction;

window matching;

image processing;

hashing;

waits;

retries;

context cancellation semantics;

input syntax;

key-name parsing;

CLI;

tracing;

backend selection;

provisioning;

protocol compatibility;

error presentation;

application lifecycle.

If substantial application logic begins accumulating in GJS, stop and re-evaluate.

Testing Strategy

1. Protocol unit tests

Use fake D-Bus services from Go.

Test:

capability discovery;

protocol compatibility;

method encoding/decoding;

error translation;

service disappearance;

context cancellation;

close behavior.

2. Capability adapter tests

Test each GNOME-native adapter independently:

windows;

screen;

input;

clipboard.

3. Extension logic tests

Factor serialization and translation helpers so they can be tested outside a full desktop session where practical.

Do not mistake pure JavaScript tests for integration coverage.

4. Real GNOME integration suite

A real controlled GNOME Wayland session is the key evidence.

Window test

launch app;

discover it;

get by ID;

activate;

move;

resize;

minimize;

restore;

maximize;

fullscreen;

unfullscreen;

close;

observe lifecycle events.

Screen test

display deterministic content;

capture full screen;

capture region;

verify bounds;

verify known pixels/hash.

Input test

type ASCII into native Wayland app;

type Unicode;

modifier combinations;

key holds;

pointer move;

click;

button holds;

all scroll directions;

pointer position.

Clipboard test

set via perfuncted;

read in another app;

set externally;

get via perfuncted;

Unicode;

large text.

Cross-capability test

Exercise the product end-to-end:

launch app
    ↓
find window
    ↓
activate
    ↓
type text
    ↓
copy
    ↓
read clipboard
    ↓
take screenshot
    ↓
locate visual result
    ↓
click it

This is the most important acceptance scenario.

Phase 0: Technical Feasibility Spike

Before productionizing the integration, prove all risky privileged operations with a tiny throwaway extension.

The spike must demonstrate:

Go ↔ extension D-Bus communication.

Window enumeration/control through Meta.Window.

Virtual keyboard creation.

Typing into a native Wayland application.

Unicode typing.

Virtual pointer creation.

Clicking a native Wayland application.

Scrolling.

Shell screenshot capture.

Screenshot transport via Unix FD into Go.

Clipboard get/set.

Current pointer position.

If any of the key privileged primitives cannot work reliably on supported GNOME versions, revise the architecture before building production abstractions.

This phase exists to falsify the design cheaply.

Production Implementation Plan

Phase 1 — GNOME bridge foundation

Add:

gnome-extension/
internal/gnomebridge/

Implement:

extension enable/disable lifecycle;

D-Bus ownership;

Core interface;

protocol version;

capability reporting;

common errors;

Go bridge client.

No public behavior change yet.

Phase 2 — Windows

Implement:

complete window interface;

stable IDs;

window state;

all controls;

fullscreen parity;

lifecycle/focus signals;

GnomeNativeManager;

extension-first GNOME selection.

This subsumes issue #117.

Phase 3 — Input

Implement:

virtual keyboard;

Unicode text;

named keys;

modifiers;

key holds;

virtual pointer;

absolute move;

button operations;

scrolling;

pointer location;

optional batching.

Make it the preferred GNOME input backend.

Phase 4 — Screen

Implement:

Shell.Screenshot;

full capture;

region capture;

Unix-FD transport;

GnomeNativeScreenBackend.

Make it the preferred GNOME screen backend.

Phase 5 — Clipboard

Implement:

GetText;

SetText;

GnomeNativeClipboardBackend.

Make it the preferred GNOME clipboard backend.

Phase 6 — Provisioning

Implement:

embedded extension;

install detection;

atomic update;

enablement;

protocol compatibility;

stale-version handling;

typed relogin-required state;

pf info diagnostics.

Phase 7 — Packaging

Support:

release binaries;

go install;

native packages;

Flatpak where practical.

Ensure the D-Bus policy is correct for Flatpak.

Phase 8 — Integration CI

Add a controlled GNOME Wayland integration environment and run complete cross-capability tests.

The GNOME backend should not be considered complete without this.

Phase 9 — Reassess GNOME capability paths

Once the bridge has adequate evidence and field experience:

Verify that the native bridge remains preferred and that the other GNOME
paths are documented as explicit capabilities for sessions that need them.
Remove a path only when it no longer serves an independently supported runtime
capability:

GNOME Shell Eval window backend;

GNOME Shell screenshot backend;

GNOME setup instructions that no longer describe a supported path;

GNOME-specific uinput setup expectations that no longer apply;

GNOME-specific wl-clipboard dependency that no longer applies.

Retain generic compositor implementations for their supported environments.

Logical Commit Series

A sensible implementation sequence:

gnome: add bundled native integration and protocol

gnome: add full native window backend

gnome: add native virtual input backend

gnome: add native screen capture backend

gnome: add native clipboard backend

gnome: provision bundled integration automatically

flatpak: allow bundled GNOME integration

integration: exercise full GNOME automation stack

docs: make GNOME a first-class supported platform

gnome: retire obsolete unsafe-mode paths

Each commit should leave the tree buildable and tested.

Acceptance Criteria

The project is complete when all of the following are true.

Installation

Perfuncted contains the GNOME extension in its normal release artifact.

No separate network download is needed.

The user does not manually install or manage the extension.

A first-session activation requirement is detected and reported explicitly.

After activation, normal use requires no GNOME-specific setup.

Windows

Window discovery works without Shell.Eval.

Window IDs are stable for the current session.

Activate works.

Move works.

Resize works.

Minimize works.

Maximize works.

Restore works.

Fullscreen works.

Unfullscreen works.

Close works.

State and geometry are populated.

Lifecycle/focus events work.

Screen

Full-screen capture works without unsafe mode.

Region capture works.

No portal prompt occurs in the normal bridge path.

Screenshot transport does not expose arbitrary filesystem writes.

Existing image/hash APIs work unchanged above the new backend.

Input

ASCII typing works in native Wayland applications.

Unicode typing works.

Named keys work.

Modifier combinations work.

Key-down/up work.

Pointer movement works.

Click works.

Mouse-down/up work.

Vertical and horizontal scrolling work.

Pointer-location query works.

No /dev/uinput access is required in the normal path.

Clipboard

Clipboard text get works.

Clipboard text set works.

No wl-clipboard package is required in the normal GNOME path.

General

perfuncted.Open can require all five capabilities successfully on supported GNOME Wayland after extension activation.

pf info clearly identifies GNOME-native backends.

Protocol mismatches have explicit diagnostics.

No arbitrary JavaScript or generic privileged RPC exists.

A real GNOME integration test exercises a complete automation workflow.

Definition of “Just Works”

Take a stock supported GNOME Wayland installation.

Do not:

enable unsafe mode;

install wl-clipboard;

configure uinput;

add the user to the input group;

grant a screen-capture portal session;

manually install a perfuncted companion component.

Install perfuncted.

If the GNOME extension was not already present in the running Shell session, perfuncted may require one logout/login because GNOME must load the newly installed extension.

After that session boundary:

session, err := perfuncted.Open(
    ctx,
    perfuncted.Require(
        perfuncted.CapabilityScreen,
        perfuncted.CapabilityInput,
        perfuncted.CapabilityWindows,
        perfuncted.CapabilityOutputs,
        perfuncted.CapabilityClipboard,
    ),
)

must succeed, and every documented operation for those capabilities must work.

That is the product target.

Decision

Proceed with the expanded GNOME integration rather than a window-only extension.

A window-only extension mostly replaces an existing unsafe mechanism and has a questionable complexity/value ratio.

A unified GNOME bridge materially changes the platform:

Before

windows     unsafe mode / Shell.Eval
screen      unsafe mode or portal consent
input       uinput permission / fallback complexity
clipboard   external utility
outputs     works

becomes:

After

windows     GNOME-native
screen      GNOME-native
input       GNOME-native
clipboard   GNOME-native
outputs     Wayland-native

using one bundled, narrow, versioned GNOME integration.

The intended product promise is:

Install perfuncted. On GNOME Wayland, perfuncted automatically uses its bundled GNOME integration to provide complete desktop automation. A first-time live installation may require one logout/login because GNOME only loads newly installed local extensions at session start; after that, no GNOME-specific setup is required.
