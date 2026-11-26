# Go library

Perfuncted is also available as a Go library for direct desktop automation.

```bash
go get github.com/nskaggs/perfuncted/v2
```

```go
import "github.com/nskaggs/perfuncted/v2"

func enter() error {
    s, err := perfuncted.OpenDefault()
    if err != nil { return err }
    defer s.Close()
    return s.Do(perfuncted.Type("Invoice {A} + tax"),
        perfuncted.Key("tab"), perfuncted.Type("12.34"))
}
```

By default, `OpenDefault` requires input before returning and may prompt for Wayland
authorization. Request additional capabilities with `Require` or `Optional`.
`Type` sends literal text; `Key` sends a named key, `Hotkey` sends a chord,
and `TypeKeys` explicitly interprets key syntax. The batch targets the
currently focused application and stops at the first error. A successful
dispatch does not prove the application accepted or saved the input.

On X11, literal Unicode text can fail when the character is absent from the active
X keymap; this is an input-backend limitation, not a shortcut parse.

For an exact-title, window-targeted sequence, request the window capability:

~~~go
s, err := perfuncted.OpenDefault(perfuncted.Require(perfuncted.CapabilityWindows))
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

For example, `s.Input.Type(ctx, text)` becomes `s.Input().Type(ctx, text)`. Variables and parameters holding pointers to capability bundles should now hold concrete bundle values. Bundles are non-owning handles; they cannot be assigned back into a session. Use `s.Has(capability)` or `s.Capability(capability)` for availability instead of comparing bundles to nil.

`Open`, `OpenDefault` and `NewSessionForTesting` still return `*Session`. Copying a Session value aliases the private session; closing either copy closes the same session. Overwriting a wrapper does not redirect previously issued handles. Retain the original session reference and close it when finished.

`*Window` and `*Application` retain their pointer-shaped APIs. Copies preserve their original target or process identity; replacing a public wrapper does not replace the owning session's resource. A zero Session cannot authorize launching a process.

Existing root names for capture metadata, window events, motion options, input receipts and accessibility action receipts remain available. Canonical capture metadata lives in the screen package, window events in the window package and semantic action receipts in the accessibility package. For example, `perfuncted.AccessibilityActionReceipt` aliases `accessibility.ActionReceipt`. Existing root names remain usable.

To return to v1, restore the original module path, version and capability field calls. Existing v1 tags and installed CLI releases are unchanged.
