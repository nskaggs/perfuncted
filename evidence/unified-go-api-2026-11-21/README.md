# Unified Go API ergonomics — implementation evidence

Date: 2026-11-21. Baseline: `2338a866c220a2ccf914518ef23805ea021ad5eb` on the canonical Perfuncted `main` checkout.

## Delivered scope (M1 plus documentation)

- `OpenDefault` opens the existing Session with input required by default; an explicit `Optional(CapabilityInput)` is respected, and unrelated capabilities remain unrequested.
- Non-owning, immutable `SessionView` derived with `WithContext` and `WithTimeout`; Session continues to own and close backend resources.
- `Do` executes ordered input steps (`Type`, `Key`, `Hotkey`, `TypeKeys`, `Click`, `Delay`) after preflight. Literal text is never interpreted as shortcuts; errors preserve causes with `StepError`, and no operation is silently retried.
- Added cancellation-aware parsing for existing input paths, bounded named-key expressions and named-key/chord preflight, and portable key semantics against the shared keymap (not a single platform's XTEST table). XTEST's ordinary navigation/key names were extended.
- README, compile-checked Go example, synthetic tests, and ten-step matched benchmark.

## Validation actually executed

- `just precommit`: PASS after the final changes, including generated CLI/API/doc synchronization and vet.
- `CGO_ENABLED=0 go test -short ./... -count=1`: PASS after the final changes.
- `just test-race`: PASS after the final changes.
- `just test-integration-headless-x11`: PASS with a controlled editor verifying literal braces, plus signs, and shortcut-looking text.
- `just test-integration-headless-wayland`: PASS with controlled editor and Unicode `é` in the literal marker.
- `just test-session`, `just test-integration-backends`, `just test-accessibility-certification`, `just test-release`: PASS.
- Full `just lint`: FAIL on **55 findings in preexisting, unrelated files**, with **no findings in task-owned Go files** in the final report. No lint rules disabled and no unrelated files changed to hide failures. Pinned lint tools were installed into task-owned `/var/tmp/pf6r21/bin` because existing agent-nice wrapper lacked its underlying executable.

### Ten-step benchmark

Same in-memory backend, ten literal-text actions per iteration; measurements isolate wrapper overhead, not desktop throughput:

| Path | Time | Bytes/op | Allocations/op |
|---|---:|---:|---:|
| Batch `Session.Do` | 17,927–18,596 ns | 4,396 | 87 |
| Direct `Input.TypeLiteral` | 13,199–13,638 ns | 3,804 | 80 |

No remote desktop or privileged authorization was used in the benchmark.

## Explicit deferrals and limitations

- **Window-targeted `WindowTarget.Do` (M2)** was not exposed. The current native window ID / refreshed `Window.Info` does not establish stable instance identity after an ID is recycled, so it cannot meet the plan's fail-closed replacement-window acceptance criterion. Unscoped `Session.Do` deliberately targets current desktop focus and does not promise a particular window.
- **Additional verified convenience (M3)** was not added because the existing `AccessibilityLocator.ClickAndWait` / `FillAndWait` and wait/evidence APIs already supply scoped postconditions and receipts; a second wrapper without proven authority would add surface without achieving the proposed safety semantics.
- **X11 Unicode** remains limited by the active X server keymap. The controlled XTEST environment cannot emit an unmapped `é`; unlike Wayland, this is not evidence of universal Unicode support. No clipboard fallback, privilege bypass, or keymap mutation was introduced.
- No attended physical GNOME/KDE consent-prompt acceptance was conducted. Headless protocol tests are not substitutes for it.
- The shared checkout has **separate in-progress** CI, `go.mod` (Go 1.27.4), `justfile`, and EIS coverage changes. They are excluded from this implementation's commit; the accepted `origin/main` before commit specified Go 1.27.3.

The key outcome is a compact, safer Go scripting layer over the existing authoritative Session and InputBundle implementation, with observable error and cancellation semantics, rather than an unverified automation framework.
