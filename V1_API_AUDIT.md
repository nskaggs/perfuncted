# Perfuncted v1 public API audit

This is the disposition record for the `V1_PLAN.md` public-contract freeze.
The supported import path is `github.com/nskaggs/perfuncted`; the backend and
algorithm packages listed below remain public because they are used by the
session contract, integration tests, or the supported Snatchblock consumer.
Packages under `internal/` are implementation-only.

## Package disposition

| Package | Disposition | v1 reason |
| --- | --- | --- |
| `perfuncted` | retain/revise | Canonical session, resource, capability, wait, option, and typed-error contract. |
| `screen` | retain | `Screenshotter` and runtime backend contract used by screen capabilities and downstream adapters. |
| `input` | retain | `Inputter` and runtime backend contract used by input capabilities. |
| `window` | retain/revise | Window discovery, stable IDs, control interfaces, and backend support reporting. |
| `output` | retain | Read-only output enumeration contract. |
| `clipboard` | retain | Clipboard get/set backend contract. |
| `find` | retain | Pixel matching/wait algorithms consumed by screen and Snatchblock. |
| `poll` | retain | Generic polling consumed by `find` and downstream wait code. |
| `transport` | retain | Retry classification helpers consumed by Snatchblock transport handling. |
| `pftest` | retain | Deterministic test fixtures; not a production backend. |
| `ctxutil` | remove/private | Historical implementation helper; replaced by module-local `internal/contextutil`. |
| `cmd/pf` | retain as application | CLI surface, generated from the canonical root API. |
| `scripts` | retain as development tooling | Generator and API verification tooling, not library API. |
| `internal/*` | private | Backend probes, transports, runtime, and resource helpers. |

## Symbol disposition

The following is the complete top-level symbol disposition. Exported methods
and fields on retained types follow the same disposition as their containing
type unless called out in the breaking changes below.

### Root package: retain

`Session`, `Application`, `Window`, `WindowID`, `WindowMatch`, `Command`,
`SessionConfig`, `DesktopTarget`, `TargetKind`, `Capability`,
`CapabilityStatus`, `CapabilityError`, `OperationError`, `Condition`,
`WaitOption`, `Option`, `SessionDetection`, `Open`, `DetectSession`,
`EnvironmentTarget`, `WithTarget`, `WithHeadless`, `WithNested`,
`WithTrace`, `WithTraceDelay`, `WithTraceOut`, `WithLogger`, `Require`,
`Optional`, `NewSessionForTesting`, `CleanupStaleSessions`, `All`, `Any`,
`Not`, `Predicate`, `WindowExists`, `WindowClosed`, `WaitEvery`, and the
capability constants are retained/revised as documented by their Go comments.

The root capability facades retain their exported operation methods. Their
operation checks now use the selected backend's published operation report.
Returned slices, environments, target snapshots, and capability operation
lists are defensive copies.

`Session.Runtime` is removed from the v1 surface because it returned the
implementation-only `internal/env.Runtime` type. Callers that need routing
information use `Session.Target`, `Session.Env`, or the specific display and
runtime accessors instead. `Session.LogPath` and `Session.SwaySocket` remain
documented diagnostic accessors for managed sessions; they do not grant
ownership of the returned paths.

### Backend and algorithm packages: retain

The exported interfaces, backend constructors, probe functions, result types,
error sentinels, and algorithm functions in `screen`, `input`, `window`,
`output`, `clipboard`, `find`, `poll`, and `transport` are retained where they are used by
the root contract or supported downstream code. Concrete backend operation
reports are implementation-facing extensions and are authoritative for root
capability status.

`pftest` retains only deterministic fixtures used by tests and examples; it is
not presented as a production backend.

### Removed or retyped symbols

| Historical symbol | v1 disposition | Migration |
| --- | --- | --- |
| `ctxutil.Default` | remove from public contract | Use each consumer's local private `internal/contextutil.Default`; no external import is supported. |
| `DetectSession() (string, map[string]string)` | retype | Use `SessionDetection` fields (`Kind`, `XDGRuntimeDir`, `WaylandDisplay`, `DBusAddress`). |
| `ErrNotAvailable` | remove | Use `ErrUnavailable` and `errors.Is`. |
| `Session.Runtime() internal/env.Runtime` | remove | Use `Target`, `Env`, `XDG`, `WaylandDisplay`, `DBusAddress`, or `X11Display`; the internal runtime type is not a v1 contract. |
| backend-specific unsupported strings | revise | Root facade callers use `ErrUnsupported`; backend packages retain their own lower-level sentinel for direct backend callers. |

No compatibility aliases or parallel old/new root APIs are retained.

## Contract decisions

- `Open`, `Launch`, `Wait`, `Application.Wait`, and `Application.Stop` reject
  nil contexts with `ErrInvalidArgument`; backend helper APIs normalize nil
  contexts privately for legacy low-level behavior.
- `Close` is idempotent, waits for the first close operation, and owns child
  applications and backends created through the session. A closed session's
  capability methods return `ErrSessionClosed`; child window handles cannot be
  used through another session.
- `Application` is a session-owned process-group handle. Launch context only
  governs startup; callers must stop or close the owning session to terminate
  it. `Wait` may be called repeatedly and concurrently.
- `Window` is a stable session-bound handle with a cached title snapshot and
  authoritative refresh through `Info`. It must not be retained for use after
  its session closes.
- `CapabilityStatus.Operations` is a snapshot. `Supports` answers what the
  active backend reports as callable. Facades reject absent operations with
  `ErrUnsupported`; they do not turn unsupported work into successful no-ops.
- `ErrUnavailable`, `ErrUnsupported`, `ErrInvalidArgument`,
  `ErrOperationFailed`, `ErrSessionClosed`, and `ErrNilSession` are the stable
  root categories. Backend causes remain discoverable through wrapping and
  `errors.Is`/`errors.As`.

## Verification

The audit is paired with compile/behavior coverage in `session_v1_test.go`,
`session_window_v1_test.go`, `perfuncted_close_test.go`, the capability bundle
tests, and `example_test.go`. Release checks are recorded in the final task
handoff; release tagging and pushing are deliberately outside this change.
