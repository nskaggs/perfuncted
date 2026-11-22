# Unified Go API ergonomics — implementation evidence

Date: 2026-11-21. Baseline: `2338a866c220a2ccf914518ef23805ea021ad5eb` on the canonical Perfuncted `main` checkout.

## Delivered scope (M1 and M2 plus documentation)

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
- Full lint: **PASS, 0 issues**. Corrected real error handling and shadowing issues, test assertions and narrowly scoped test-fixture lint rules; security-sensitive complex production functions retain documented local exclusions. Pinned tooling is in /var/tmp/pf6r21/bin.

### New scripting API test coverage

- convenience.go: **153/153 statements (100%)**.
- convenience_window.go: **54/54 statements (100%)**.
- input/keysend.go: **89/89 statements (100%)**.
- A permanent Go-only test gate, just coverage-convenience, is part of just quality and the reusable CI quality workflow. It fails when any production statement in any of these files becomes uncovered.
- New tests cover option validation, all step constructors/dispatch paths, literal syntax, cancellation, session closure, error wrapping, malformed input, window ambiguity/identity/focus failures, title changes, activation rejection, and timeouts.
- Overall repository coverage is below 100%; the scoped 100% gate applies only to the new scripting layer and its context-aware input parser.

### Ten-step benchmark

Same in-memory backend, ten literal-text actions per iteration; measurements isolate wrapper overhead, not desktop throughput:

| Path | Time | Bytes/op | Allocations/op |
|---|---:|---:|---:|
| Batch `Session.Do` | 17,927–18,596 ns | 4,396 | 87 |
| Direct `Input.TypeLiteral` | 13,199–13,638 ns | 3,804 | 80 |

No remote desktop or privileged authorization was used in the benchmark.

## Explicit deferrals and limitations

- **Window-targeted M2 is delivered.** Exact-title selection, unique stable-handle resolution, activation and fresh focus/identity checks precede each input step. Window-relative pointer safety is not guaranteed; global-coordinate clicks are rejected in targeted batches. Focus cannot be locked atomically, and recycled IDs with identical PID/AppID/Class metadata cannot be distinguished; those limits are documented.
- **Additional verified convenience (M3)** was not added because the existing `AccessibilityLocator.ClickAndWait` / `FillAndWait` and wait/evidence APIs already supply scoped postconditions and receipts; a second wrapper without proven authority would add surface without achieving the proposed safety semantics.
- **X11 Unicode** remains limited by the active X server keymap. The controlled XTEST environment cannot emit an unmapped `é`; unlike Wayland, this is not evidence of universal Unicode support. No clipboard fallback, privilege bypass, or keymap mutation was introduced.
- No attended physical GNOME/KDE consent-prompt acceptance was conducted. Headless protocol tests are not substitutes for it.
- The separately prepared Go 1.27.4 workflow/toolchain updates and EIS descriptor regression test were preserved throughout the work and included in the final stabilization validation.

The key outcome is a compact, safer Go scripting layer over the existing authoritative Session and InputBundle implementation, with observable error and cancellation semantics, rather than an unverified automation framework.
