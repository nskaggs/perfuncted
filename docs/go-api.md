# Go library

Perfuncted is also available as a Go library for direct desktop automation.

```bash
go get github.com/nskaggs/perfuncted/v2
```

```go
import "github.com/nskaggs/perfuncted/v2"

func enter() error {
    s, err := perfuncted.Open()
    if err != nil { return err }
    defer s.Close()
    return s.Do(perfuncted.Type("Invoice {A} + tax"),
        perfuncted.Key("tab"), perfuncted.Type("12.34"))
}
```

By default, `Open()` attempts input, screen, windows, outputs and clipboard
noninteractively on a best-effort basis. Accessibility and input observation are opt-in.
Explicit Require or Optional options replace the default set, including an empty
selection. Use Require when a capability must be available.
`Type` sends literal text; `Key` sends a named key, `Hotkey` sends a chord,
and `TypeKeys` explicitly interprets key syntax. The batch targets the
currently focused application and stops at the first error. A successful
dispatch does not prove the application accepted or saved the input.

On X11, literal Unicode text can fail when the character is absent from the active
X keymap; this is an input-backend limitation, not a shortcut parse.

For an exact-title, window-targeted sequence, request the window capability:

~~~go
s, err := perfuncted.Open(perfuncted.Require(perfuncted.CapabilityWindows, perfuncted.CapabilityInput))
if err != nil { return err }
defer s.Close()
return s.Window("Editor").Do(perfuncted.Type("Hello"), perfuncted.Key("tab"))
~~~

Window-targeted batches select exactly one window, activate it once, wait for
observed focus, and recheck the selected identity and active state before
each input step. Missing or ambiguous windows, missing identity metadata,
unsupported focus observation and focus loss fail closed without redirecting
subsequent input. Window focus is not an atomic lock, and a recycled native
ID with identical published metadata cannot be distinguished. For stronger
semantic scoping and independently verified outcomes, use
AccessibilityLocator.ClickAndWait or FillAndWait with a postcondition.

Use `s.WithContext(ctx).Do(...)` or `s.WithTimeout(duration).Do(...)` for a
bounded batch. `StepError` identifies the failed step and unwraps its cause.
For receipts, explicit desktop configuration, accessibility transactions, and
other advanced control, use the existing session bundles. In particular,
`AccessibilityLocator.ClickAndWait` returns action and observation evidence;
conditions such as `AccessibilityTextContains` can express a postcondition.

See the [Go API reference](https://pkg.go.dev/github.com/nskaggs/perfuncted/v2).

## Migrating from v1 to v2

The v2 development module deliberately changes Go source compatibility to prevent callers from overwriting session-owned backends and process state. CLI commands, flags, output contracts and workflow formats are unchanged. Existing v1 consumers can remain pinned to `github.com/nskaggs/perfuncted@v1.4.0`. To test unreleased v2 code, pin the desired commit explicitly.

Change the module requirement and all Perfuncted imports (including subpackages) to `github.com/nskaggs/perfuncted/v2`. Replace the seven session capability fields with accessor calls:

| v1 field | v2 accessor | Return type |
| --- | --- | --- |
| `s.Screen` | `s.Screen()` | `ScreenBundle` |
| `s.Input` | `s.Input()` | `InputBundle` |
| `s.InputObservation` | `s.InputObservation()` | `InputObservationBundle` |
| `s.Windows` | `s.Windows()` | `WindowBundle` |
| `s.Outputs` | `s.Outputs()` | `OutputBundle` |
| `s.Clipboard` | `s.Clipboard()` | `ClipboardBundle` |
| `s.Accessibility` | `s.Accessibility()` | `AccessibilityBundle` |

For example, `s.Input.Type(ctx, text)` becomes `s.Input().WithContext(ctx).TypeKeys(text)`. Variables and parameters holding pointers to capability bundles should now hold concrete bundle values. Bundles are non-owning handles; they cannot be assigned back into a session. Use `s.Has(capability)` or `s.Capability(capability)` for availability instead of comparing bundles to nil.

`Open` and `NewSessionForTesting` still return `*Session`. Copying a Session value aliases the private session; closing either copy closes the same session. Overwriting a wrapper does not redirect previously issued handles. Retain the original session reference and close it when finished.

`*Window` and `*Application` retain their pointer-shaped APIs. Copies preserve their original target or process identity; replacing a public wrapper does not replace the owning session's resource. A zero Session cannot authorize launching a process.

Domain types now have one canonical public spelling. Replace the former root aliases and companion constants in callers (including signatures, struct literals, and errors.Is checks):

| Former root name | Final v2 name |
| --- | --- |
| `MotionOptions`, `ValidateMotionOptions` | `input.MotionOptions`, `input.ValidateMotionOptions` |
| `InputActionReceipt` | `input.ActionReceipt` |
| `RemoteDesktopInputOptions` | `input.RemoteDesktopOptions` |
| `Capture`, `WindowCapture`, `WindowCaptureMode`, `WindowCaptureState` | Corresponding `screen` types |
| `WindowCapture*` constants and `ErrWindowCaptureMinimized`/`ErrWindowCaptureUnmapped` | Corresponding `screen` constants and errors |
| `WindowMatch` | `window.Match` |
| `WindowEvent`, `WindowEventKind` and window event constants | `window.Event`, `window.EventKind` and `window` constants |
| `AccessibilityActionReceipt` | `accessibility.ActionReceipt` |
| `ActionOutcomeStatus`, `ActionOutcomeProof`, and `ActionOutcome*` constants | Corresponding `accessibility` names |

The seven capability accessors, session-specific errors, WindowID, Window, Application and wait/convenience contracts retain their root names. These are source changes within unreleased v2; the CLI and serialized data formats remain unchanged.

To return to v1, restore the original module path, version and capability field calls. Existing v1 tags and installed CLI releases are unchanged.

### Operation scopes and startup

`Open(opts...)` accepts optional `WithStartupContext(ctx)` for the startup
phase only. A successful session has its own lifetime. Use `WithHeadless()`
or `WithNested()` for default isolated desktop configuration, or supply one
typed configuration value.

Each session-bound handle supports `WithContext(ctx)` and `WithTimeout(d)`.
They copy the handle's settings, not the desktop or process. Returned windows,
applications and locators inherit their selected scope. A positive timeout
replaces the ordinary operation default; zero restores that default. An
earlier caller deadline still wins, and backend safety limits remain in force.
Durations start at each call, not when the scope is created. An explicit
batch override covers the whole batch. Nil contexts and negative durations
are invalid. Closing any scoped Session closes the shared desktop, even
when its caller context has expired.

`Type(text)` on the session or input handle is literal. Use
`Input().TypeKeys(sequence)` for interpreted syntax, and `Input().Click(x,y)`
for a left click (an optional button selects a different button). Use
`Windows().Find(window.Title("Editor"))` for a unique exact-title target.
An explicit zero-value window.Match{} matches any window; Find and Wait still require exactly one result. A blank window.Title helper is invalid. List() lists all windows.
Use a rich window.Match for other predicates. No match or ambiguity is an
error, not permission to act on an arbitrary target.

`Screen().Grab()` captures the whole desktop as an image; an explicit
rectangle selects a region. `Capture(rect)` retains region metadata.
Whole-desktop metadata is supported only when the backend can establish its
desktop rectangle authoritatively; otherwise Capture() returns an
unsupported error and the caller can use Grab().

Long-lived subscriptions do not inherit a short operation deadline. A
positive timeout override bounds setup; the established stream is governed
by its explicit caller context and session lifetime. Window events are bounded, lossy notifications distributed by one session-owned reader to independent subscribers. A slow or canceled subscriber does not consume another subscriber's notifications. Canceling a scope closes only its subscription and discards unread notifications when it retires. Session or provider shutdown retires all subscriptions. Notifications already received cannot be recalled. Use fresh List or Info queries for authoritative state.

Verified accessibility transactions use the same overall deadline for target
resolution, dispatch and independent verification. Explicit state-wait
durations are not silently capped by the ordinary Long default; an earlier
explicit scope or parent deadline still applies. Failure bundles use a Long
overall default (or scoped override) and preserve the separate configured
Diagnostic budget for each backend artifact, so one stalled provider cannot
consume the entire collection budget. Diagnostic collection remains best effort,
including when gathering evidence after a failure.
