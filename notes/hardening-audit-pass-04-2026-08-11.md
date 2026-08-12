# Perfuncted hardening audit pass 4/10

- Repository: `/home/nskaggs/workspace/perfuncted`
- Prompt: `/home/nskaggs/workspace/codex/prompts/code-hardening-audit.md` v1.2.0
- Baseline: `49eeb84b59b393bc0694257940f2b6c850cf03db` on `main`
- Code-fix commit: `4e415ac` (`fix: release XTEST modifiers on input errors`)
- Audit-note commit: recorded in the final handoff after this note is committed

## Git and prior coverage

- The target started clean on `main`; `git status --porcelain=v1` was empty.
- `git rev-parse --show-toplevel` returned the nested target, confirming the
  intentional parent-workspace rooting.
- Pass 1 covered managed process-group descendant cleanup; pass 2 covered CLI
  and diagnostic privacy redaction; pass 3 covered Sway IPC framing and
  response validation. Their partitions and fixes were not re-audited here.
- Prior notes consulted: `notes/hardening-audit-pass-01-2026-08-11.md`,
  `notes/hardening-audit-pass-02-2026-08-11.md`, and
  `notes/hardening-audit-pass-03-2026-08-11.md`. Recent input history and the
  XTEST, uinput, and Wayland keyboard siblings were also inspected.

## Git-write preflight

Before source edits, the following passed against the nested target:

- `git rev-parse --show-toplevel`, `git branch --show-current`,
  `git status --porcelain=v1`, `git rev-parse HEAD`, and
  `test ! -e .git/index.lock` confirmed the target root, `main`, clean tree,
  baseline `49eeb84`, and no stale index lock.
- A disposable `GIT_INDEX_FILE` under `.git` was initialized with
  `git read-tree HEAD`, written with `git write-tree`, used with
  `git update-index --add --cacheinfo` to stage a virtual probe, written again,
  reset with `git read-tree HEAD`, and written a final time. The before and
  after trees matched `HEAD^{tree}`; the probe tree differed as expected.
- `git update-ref refs/heads/main HEAD HEAD` passed without changing the ref.
- `git commit --dry-run --allow-empty --no-verify -m 'audit preflight: no content'`
  reported `nothing to commit, working tree clean`.
- Workspace-local Go cache/temp directories used for validation were created by
  this pass and removed before staging.

## Scope and hypotheses

This pass selected the X11/XTEST input backend's temporary modifier lifecycle,
distinct from the prior process, diagnostic, and Sway protocol partitions.
The inspected boundary was `XTestBackend.Type` and literal-text handling in
`input/xtest.go`, with sibling parity checks against the uinput and Wayland
keyboard implementations.

Hypotheses:

1. A failed XTEST combo key event could leave temporary Ctrl/Shift/Alt/Super
   modifiers pressed.
2. A failed temporary modifier press could leave earlier modifiers pressed.
3. Cancellation during the combo dwell or a shifted literal could leave the
   temporary modifier pressed.

## Finding and fix

### H-001 — medium severity, high confidence: XTEST errors left temporary modifiers held

- Paths: `input/xtest.go`, `input/xtest_test.go`, and
  `internal/x11/mock.go`.
- Behavior: `XTestBackend.typeContext` pressed combo modifiers and returned
  directly on modifier setup, key delivery, sleep/cancellation, or release
  errors. The literal-text path had the same gap for temporary Shift.
- Impact: a failed or cancelled automation action could leave Ctrl, Shift,
  Alt, or Super logically pressed in the target X11 desktop, contaminating
  subsequent user or automation input.
- Baseline evidence: before the production change,
  `GOCACHE=/home/nskaggs/workspace/perfuncted/.audit-cache
  GOTMPDIR=/home/nskaggs/workspace/perfuncted/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off ./input -run '^TestXTestType'
  -count=1` failed the three combo/text cleanup regressions because the
  expected release events were absent.
- Regression evidence: socket-level tests use a deterministic X11 connection
  double and assert the observable event sequence for main-key failure,
  earlier-modifier setup failure, shifted-text failure, and cancellation.
- Fix: `typeAction` tracks successfully pressed temporary modifiers and uses a
  cancellation-independent best-effort cleanup on every failed path, while
  `typeTextRune` applies the same per-rune invariant for Shift. Successful
  releases are removed from the cleanup set; a failed release is retried by
  the deferred best-effort cleanup. The invariant lives at the XTEST action
  layer, covering all current callers without changing explicit user-held
  `{key down}` semantics.
- The X11 test double gained constructors for the private cookie fixtures;
  this is test support only and does not alter the runtime X11 API.

No additional defect was promoted after the sibling parity and call-site
search. uinput already releases temporary modifiers on its error paths, and
the Wayland keyboard already has best-effort temporary-modifier cleanup.

## Validation

Passed:

- `gofmt -w input/xtest.go input/xtest_test.go internal/x11/mock.go`
- `git diff --check` and `git diff --cached --check`
- Focused fixed tests:
  `GOCACHE=/home/nskaggs/workspace/perfuncted/.audit-cache
  GOTMPDIR=/home/nskaggs/workspace/perfuncted/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off -short -count=1 ./input -run
  '^TestXTestType'` — four tests passed.
- Focused race tests:
  `GOCACHE=/home/nskaggs/workspace/perfuncted/.audit-cache
  GOTMPDIR=/home/nskaggs/workspace/perfuncted/.audit-tmp GOWORK=off
  CGO_ENABLED=1 go test -race -p=1 -vet=off -short -count=1 -v ./input
  -run '^TestXTestType'` — four tests passed.
- Adjacent non-socket input tests covering XTEST, uinput, parser, context,
  open/probe, and validation paths passed with the equivalent serialized
  command:
  `GOCACHE=/home/nskaggs/workspace/perfuncted/.audit-cache
  GOTMPDIR=/home/nskaggs/workspace/perfuncted/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off -short -count=1 ./input -run
  '^(TestXTestType|TestUinput|TestParse|TestSleep|TestNormalize|TestOpenRuntime|TestProbe|TestCheck|TestValidate)'`.
- Compile-only validation:
  `GOWORK=off CGO_ENABLED=0 go test -p=1 -vet=off -run '^$' ./input
  ./internal/x11` passed; this is package compilation, not test execution.
- `GOWORK=off CGO_ENABLED=0 go vet -p=1 ./input ./internal/x11` passed.
- `gofmt -l input/xtest.go input/xtest_test.go internal/x11/mock.go` produced
  no output.
- No performance, generated-code, fuzz, benchmark, profile, integration, or
  live-X11 claim is made for this partition.

Environment/tooling limitations:

- The broader `GOWORK=off CGO_ENABLED=0 go test -p=1 -vet=off -short -count=1
  ./input` executed but four unrelated Wayland socket tests failed during
  setup with `setsockopt: operation not permitted`; this is a sandbox resource
  failure, not a failure in the changed XTEST tests.
- `staticcheck`, `golangci-lint`, `govulncheck`, and `deadcode` were not
  installed and were unavailable. Integration/display-server validation was
  not run.
- The command channel emitted repeated sandbox `Failed to create stream fd:
  Operation not permitted` warnings while still returning readable command
  results; these were treated as tooling noise and not as code evidence.

## Remaining risks

- No real X11/XTEST or Xvfb acceptance run was possible in this sandbox.
- Mouse-button cleanup, explicit user-held keys that intentionally span calls,
  and other input backends were not changed in this pass.
- If an X server reports an error after processing a key-release request, the
  cleanup retry is best-effort; the backend cannot prove remote key state from
  the XTEST error alone.

## Not inspected

Screen, output, clipboard, window, generic Wayland protocol, session PID
reuse, managed process cleanup, CLI diagnostics, integration/display servers,
release/Flatpak infrastructure, and cross-repository consumers were outside
this bounded XTEST partition or explicitly covered by prior passes.

## Prompt feedback

- Productive: the repeated-pass ledger excluded the three prior partitions and
  directed the audit to an uncovered high-risk input boundary.
- Productive: requiring failing acceptance-boundary tests exposed the false
  cleanup behavior rather than relying on static suspicion; the cancellation
  case also verified cleanup after context failure.
- Productive: the validation taxonomy kept package compile-only success,
  focused test execution, race evidence, and sandbox socket failures separate.
- Productive: the user checkpoint prevented broadening after the substantive
  defect and its adjacent error paths were closed.
- Refinement: retain the explicit reminder that cleanup tests should assert
  event sequences, not merely that an operation returned an error; this is a
  useful reusable heuristic for other input backends.

## Repository state before note commit

- Code-fix commit: `4e415ac` on direct `main`.
- The code-fix staging list contained exactly `input/xtest.go`,
  `input/xtest_test.go`, and `internal/x11/mock.go`; no concurrent writer was
  observed between baseline inspection, staging, and the code commit.
