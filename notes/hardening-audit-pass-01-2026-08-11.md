# Perfuncted hardening audit pass 1/10

- Repository: `/home/nskaggs/workspace/perfuncted`
- Prompt: `code-hardening-audit.md` v1.1.0
- Baseline: `1671f40b1148007b24c2535625e5062606533392` on `main`
- Final commit: recorded in the handoff after this note is committed

## Git and prior coverage

- The target started clean; no pre-existing work was reconciled.
- No repository-local `AGENTS.md`, `.agents/`, `.codex/`, or `notes/` guidance
  existed before this pass.
- Recent commits consulted: `1671f40` screen-capture validation,
  `ba574ce` window-match cache bound, `e73a8d6` input cleanup and cancellation,
  and `689648b` X11 capture bounds.
- Git-write preflight passed before source edits: target root and `main` were
  confirmed; `.git/index.lock` was absent; a temporary index under the target
  `.git` was read, refreshed, compared, and written as a tree matching
  `HEAD^{tree}`; `git update-ref refs/heads/main HEAD HEAD` passed; and
  `git commit --dry-run --allow-empty -m 'audit preflight: no content'`
  reported the expected clean-tree “nothing to commit” result.

## Scope and hypothesis

Inspected the public session lifecycle partition: `Open`/`Close`, managed
session infrastructure, process-group launch and shutdown, application waits,
window ownership, and session wait invalidation. The primary hypothesis was
that process-group ownership was weaker than the API lifecycle contract: a
leader could exit while a descendant remained alive, making shutdown report
success while owned work survived.

## Finding and fix

### H-001 — high severity, high confidence

- Paths: `session.go`, `perfuncted_session_test.go`
- Behavior: `managedProc.stop` treated reaping the process-group leader as
  complete without checking whether the process group still existed.
- Evidence: the regression test launches a shell that exits immediately after
  starting a TERM-ignoring child in the same process group. Before the fix,
  the test observed the child still alive after managed shutdown.
- Fix: the shared stop helper now waits for the full process group, sends
  `SIGKILL` when descendants survive the grace period, and also handles
  stale PID-file cleanup where no `*exec.Cmd` is available.
- Regression evidence: `TestManagedProcStopReapsProcessGroupChildren` fails on
  the baseline and passes after the fix. Existing leader cleanup, reverse-order
  session shutdown, and concurrent/idempotent `Close` tests also pass.

## Validation

Passed:

- `gofmt -w session.go perfuncted_session_test.go`
- `git diff --check`
- focused root lifecycle tests with `GOWORK=off`, `CGO_ENABLED=0`
- `go test . -count=1`
- `go test -race -short -count=1 .`
- `go vet .`

The broader `GOMAXPROCS=2 GOCACHE=/tmp/perfuncted-audit-pass1-all-cache
GOWORK=off CGO_ENABLED=0 go test -short -count=1 ./...` run was not a clean
repository result because unrelated socket-backed tests failed with
`setsockopt: operation not permitted`, and later package compilation failed
with `disk quota exceeded`. The changed root package passed in that run.

Not run in this pass: `just quality`, integration/display-session suites,
release/Flatpak checks, vulnerability scanning, fuzzing, benchmarks, and
profiling. No performance claim or generated-code change was made.

## Rejected and remaining risks

- No second defect was promoted without a deterministic reproducer in this
  pass. The broader audit categories remain open for later partitions.
- Stale-session cleanup still relies on PID/process-group identity from files;
  PID reuse and hostile same-user temporary-directory contents were not changed
  or independently proven here.
- Full package, CI, and live display validation remain environment-dependent.

## Coverage boundaries

Not inspected deeply: screen/input/output/clipboard backend internals, window
backend protocol implementations, CLI generation and command dispatch,
transport, integration and release infrastructure, and cross-repository
consumers. They were outside the selected lifecycle partition for this pass.

## Prompt feedback

- Productive: requiring a state invariant and a failing child-process
  reproducer exposed a real process-group leak that unit-level leader-reap
  coverage missed.
- Productive: the clean-tree and Git metadata preflight made repository safety
  explicit before edits.
- Noisy/environment-sensitive: the broad suite mixed real socket-permission
  failures with disk-quota failures, so those results must not be summarized as
  code regressions. Future passes should keep the focused acceptance boundary
  explicit before attempting the broad suite.
