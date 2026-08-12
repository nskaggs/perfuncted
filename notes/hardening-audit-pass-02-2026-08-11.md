# Perfuncted hardening audit pass 2/10

- Repository: `/home/nskaggs/workspace/perfuncted`
- Prompt: `/home/nskaggs/workspace/codex/prompts/code-hardening-audit.md` v1.1.0
- Baseline: `a042831e0969f9b9b9d927416193686856410322` on `main`
- Code-fix commit: `292930a` (`fix: redact CLI environment diagnostics`)
- Audit-note commit: recorded in the final handoff after this note is committed

## Git and prior coverage

- The target started clean on `main`; `git status --porcelain` was empty.
- The repository root resolved to `/home/nskaggs/workspace/perfuncted` and the
  parent-workspace invocation did not change the nested repository target.
- No applicable repository instruction files existed under `.agents/` or
  `.codex/`.
- Pass 1 note consulted: `notes/hardening-audit-pass-01-2026-08-11.md`.
  Pass 1 covered session lifecycle, managed applications, process groups,
  window ownership, and wait invalidation; its descendant cleanup fix is not
  re-audited here.
- Recent commits also consulted: `1671f40` screen-capture validation,
  `ba574ce` window-match cache bound, `e73a8d6` input cleanup/cancellation,
  and `689648b` X11 capture bounds.

## Git-write preflight

Before source edits, the following passed against the nested target:

- `git -C /home/nskaggs/workspace/perfuncted rev-parse --show-toplevel`
  returned the target root; branch was `main`; porcelain was empty.
- `/home/nskaggs/workspace/perfuncted/.git/index.lock` was absent.
- A temporary index under the target `.git` was read from `HEAD`, refreshed,
  written to a tree matching `HEAD^{tree}`, used to stage a disposable probe,
  reset to `HEAD`, and written again to the identical tree.
- `git -C /home/nskaggs/workspace/perfuncted update-ref refs/heads/main HEAD HEAD`
  passed without changing the ref.
- `git -C /home/nskaggs/workspace/perfuncted commit --dry-run --allow-empty
  -m 'audit preflight: no content'` reported the expected clean-tree result;
  its nonzero status was caused only by the disposable untracked probe, which
  was removed explicitly. The final preflight porcelain was empty.

## Scope and hypotheses

This pass selected the uncovered CLI/diagnostics partition. The primary
hypothesis was that machine-readable and human-readable diagnostics might
cross the process-environment trust boundary and expose inherited secrets,
session-bus credentials, or private runtime paths.

Adjacent callers inspected included `pf info`, `pf session type`, `pf session
check`, `pf session start`, `environmentMap`, and the CLI command tests. A
disconfirming search found no other raw inherited-environment emission in the
CLI beyond the intentional `session start` export path.

## Finding and fix

### H-001 — medium severity, high confidence: CLI diagnostics exposed inherited environment data

- Paths: `cmd/pf/extra.go`, `cmd/pf/docs_info_session_cmd.go`, and their CLI
  tests.
- Behavior: `pf info --output json` passed the entire `Session.Env()` map into
  its diagnostic report. `Session.Env()` is the child-process environment,
  not a diagnostic allowlist, so arbitrary inherited variables could be
  emitted. `pf session type` and `pf session check` printed raw D-Bus
  addresses and XDG/socket paths.
- Impact: piping diagnostics to logs or another process could disclose
  environment-held credentials, private bus addresses, and filesystem paths.
- Evidence: the regression test injects synthetic secret, D-Bus, and runtime
  path values and exercises the real Cobra command boundary. The pre-fix data
  flow was confirmed by tracing `Session.Env()` through `buildInfoReport`; the
  initial pre-fix test attempt could not compile because the host returned
  `disk quota exceeded` while writing Go build artifacts.
- Regression tests: `TestDiagnosticEnvironmentFiltersAndRedacts` verifies the
  allowlist and redaction helper; `TestDiagnosticsDoNotExposeSensitiveEnvironment`
  executes `info --output json`, `session type`, and `session check` and asserts
  that synthetic secret material and keys are absent.
- Fix: diagnostics now retain only display/session metadata, replace D-Bus and
  runtime paths with `<set>`, and omit inherited variables. Missing socket
  diagnostics no longer include the resolved path. `session start` continues
  to export exact generated routing values because exporting them is its
  explicit operational contract.
- Layer: the allowlist is at the shared CLI report formatter, with the two
  direct session diagnostic emitters hardened alongside it; this covers all
  current callers without changing `Session.Env()`, which must remain complete
  for child-process routing.

No additional finding was promoted after the adjacent raw-output search.

## Validation

Passed:

- `gofmt -w cmd/pf/extra.go cmd/pf/extra_test.go
  cmd/pf/docs_info_session_cmd.go cmd/pf/root_test.go`
- `git diff --check` before staging and `git diff --cached --check` before
  commit.
- `GOCACHE=<workspace disposable cache> GOTMPDIR=<workspace disposable temp>
  GOWORK=off CGO_ENABLED=0 go test -p=1 -vet=off ./cmd/pf -run
  '^(TestDiagnosticsDoNotExposeSensitiveEnvironment|TestDiagnosticEnvironmentFiltersAndRedacts)$'
  -count=1` — passed (`ok`, 0.007s). The disposable cache and temp directory
  were removed after the run.
- Direct-main commit completed as `292930a` with exactly four intended files.

Environment/resource failures and gaps:

- The first focused compile using `/tmp` failed before tests ran with
  `disk quota exceeded` while Go wrote package and vet artifacts.
- A serialized full `./cmd/pf` retry in workspace-local temp storage produced
  no usable test receipt from the execution channel; it is not counted as a
  pass or a code failure. Its disposable artifacts were removed explicitly.
- Not run: `just quality`, full unit suite, race detector, lint/deadcode,
  vulnerability scan, integration/display suites, release/Flatpak checks,
  fuzzing, benchmarks, and profiling. No performance or generated-code claim
  was made.

## Rejected and remaining risks

- `session start` still prints exact generated routing values by design; it is
  an explicit environment-export command rather than passive diagnostics.
- Capability failure strings in `pf info` may still contain backend-specific
  detail; no concrete secret-bearing failure was reproduced in this bounded
  pass, so that was not changed speculatively.
- Backend protocol implementations, transport behavior, and generated CLI
  wrappers remain outside this pass.

## Coverage boundaries

Not inspected deeply: screen, input, output, window, clipboard, and transport
backend internals; generated command implementation parity beyond the
diagnostic callers; integration and release infrastructure; and cross-repo
consumers. These were intentionally left for later passes after the requested
checkpoint rather than broadening this partition.

## Prompt feedback

- Productive: the repeated-pass coverage ledger prevented re-auditing the
  process-group fix and directed attention to a distinct trust boundary.
- Productive: requiring a real CLI boundary test exposed that the issue was
  not limited to a helper; JSON and session diagnostics had separate emitters.
- Noisy/environment-sensitive: Go compilation repeatedly encountered host
  quota/stream-receipt failures. The focused acceptance test was moved to
  explicit workspace-local temporary storage and passed; the broader result
  remains unverified rather than being mislabeled as a code regression.
- Refinement for future passes: keep diagnostic allowlist/redaction checks in
  the fast CLI gate and run broader package validation only after build-storage
  health is established.
