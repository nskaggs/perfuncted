# Perfuncted hardening audit pass 5/10

- Repository: `/home/nskaggs/workspace/perfuncted`
- Prompt: `/home/nskaggs/workspace/codex/prompts/code-hardening-audit.md` v1.2.0
- Baseline: `45ab8238c8f2ba8f5b0351a0fdd0b7b6ea51391b` on `main`
- Code-fix commit: `780832f` (`fix: retry mouse release after uncertain cleanup`)
- Audit-note commit: this note's direct-main commit, recorded in the final handoff

## Git and prior coverage

- The parent-workspace invocation was intentionally rooted to the nested target;
  `git rev-parse --show-toplevel` returned `/home/nskaggs/workspace/perfuncted`.
- The target started clean on `main`; `git status --porcelain=v1` was empty.
- Pass 1 covered managed process-group descendant cleanup; pass 2 covered CLI
  and diagnostic privacy; pass 3 covered Sway IPC framing and response
  validation; pass 4 covered X11/XTEST modifier cleanup. Those partitions were
  not re-audited here.
- Prior notes consulted: `notes/hardening-audit-pass-01-2026-08-11.md`,
  `notes/hardening-audit-pass-02-2026-08-11.md`,
  `notes/hardening-audit-pass-03-2026-08-11.md`, and
  `notes/hardening-audit-pass-04-2026-08-11.md`.
- Recent input history and sibling implementations were inspected. Existing
  tests covered cleanup after pointer movement and button-press failures, but
  not an uncertain button release in the shared bundle.

## Git-write preflight

Before source edits, the following passed against the nested target:

- `git rev-parse --show-toplevel`, `git branch --show-current`,
  `git status --porcelain=v1`, and `git rev-parse HEAD` confirmed the target
  root, `main`, clean status, and baseline above.
- `.git/index.lock` was absent.
- A disposable `GIT_INDEX_FILE` under `.git` was initialized with
  `git read-tree HEAD`; `git write-tree` matched `HEAD^{tree}`; a virtual
  `.audit-preflight-probe` was staged with `git update-index --cacheinfo` and
  produced a different tree; `git read-tree HEAD` restored the index; and the
  final tree again matched `HEAD^{tree}`.
- `git update-ref refs/heads/main HEAD HEAD` passed without changing the ref.
- `git commit --dry-run --allow-empty --no-verify -m 'audit preflight: no content'`
  returned the expected clean-tree result (`nothing to commit, working tree
  clean`, exit 1 because there was no content).

## Scope and hypotheses

This pass selected the shared mouse/button lifecycle in `InputBundle`,
specifically `DoubleClick` and `DragAndDrop`. The scope is distinct from the
prior XTEST modifier work and owns cleanup across all input backends.

Hypothesis: these operations marked the button as released before `MouseUp`
returned. If a backend accepted the press but reported an error while
releasing, the deferred cancellation-independent cleanup would be skipped and
the target could retain a logically pressed button.

## Finding and fix

### H-001 — medium severity, high confidence: release errors skipped button cleanup

- Paths: `bundle_input.go`, `bundle_input_test.go`.
- Behavior: `DoubleClick` and `DragAndDrop` set their `released` flag before
  calling `MouseUp`. A release error therefore returned without a cleanup retry.
  This is a false-cleanup state transition: the local flag claimed the remote
  button was up even though the release operation was not confirmed.
- Impact: a failed automation action could leave the primary mouse button held
  in the target desktop, contaminating later automation and user input.
- Baseline evidence: before the production change,
  `TestInputBundleDoubleClickRetriesReleaseAfterReleaseFailure`,
  `TestInputBundleDoubleClickRetriesSecondReleaseAfterReleaseFailure`, and
  `TestInputBundleDragAndDropRetriesReleaseAfterReleaseFailure` failed because
  the fake backend observed one `MouseUp` instead of the required cleanup
  retry.
- Regression evidence: `releaseRetryInput` simulates a release that returns an
  error once while recording the observable button events. The tests cover the
  first and second `DoubleClick` release and the final `DragAndDrop` release;
  they assert the original error remains visible and that cleanup retries.
- Fix: set `released = true` only after `MouseUp` succeeds, in both click
  phases and in drag completion. On an uncertain release, the existing deferred
  cleanup runs with `context.WithoutCancel(ctx)` and joins any cleanup error.
- Layer: the shared bundle owns the multi-event lifecycle and is the lowest
  common layer for all current input backends; no backend-specific duplicate
  fix was needed.

No material finding was left unfixed in this selected partition. Explicit
`MouseDown`/`MouseUp` calls remain caller-owned by the documented API contract;
backend-specific `MouseClick` methods already have their own cancellation
cleanup paths.

## Validation

Passed:

- `gofmt -w bundle_input.go bundle_input_test.go`.
- `git diff --check` and staged `git diff --cached --check`.
- Baseline regression run before the production change, using
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off . -run
  '^TestInputBundle(DoubleClick|DragAndDrop)' -count=1`: failed exactly on the
  two original release-retry assertions (`MouseUp calls = 1`).
- Focused fixed tests:
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off . -run
  '^TestInputBundle(DoubleClick|DragAndDrop)' -count=1`: passed.
- Adjacent root tests:
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off . -run
  '^Test(InputBundle|PerfunctedPaste)' -count=1`: passed.
- Full root unit tests:
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off . -count=1`: passed.
- Focused race tests:
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=1 go test -race -p=1 -vet=off . -run
  '^TestInputBundle(DoubleClick|DragAndDrop)' -count=1`: passed.
- Explicit root vet:
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go vet -p=1 .`: passed.
- Compile-only untagged repository sweep:
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off -run '^$' ./...`: passed. This is
  package compilation, not test execution.
- `git diff --check` and `gofmt -l bundle_input.go bundle_input_test.go` were
  clean. Disposable `.audit-cache` and `.audit-tmp` directories were removed
  before staging.

Environment or tooling boundaries:

- The documented `just check` recipe was not a clean receipt because its
  default Go cache under `/home/nskaggs/.cache/go-build` is read-only in this
  environment. The equivalent explicit-cache `go vet` command above passed.
- The broader unit run
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -short -p=1 -vet=off -count=1 ./...` executed, but
  `cmd/pf`, `input`, and `internal/wl` socket-backed tests failed during setup
  with `setsockopt: operation not permitted`. The changed root package and the
  other completed packages passed; this is a sandbox resource failure, not a
  changed-code assertion failure.
- Tagged compile-only validation
  `GOCACHE=$PWD/.audit-cache GOTMPDIR=$PWD/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -tags=integration -p=1 -run '^$' ./...` reached
  integration `TestMain` setup. Xvfb could not bind `/tmp/.X11-unix` and the
  session harness later reported a missing D-Bus socket. It was interrupted
  after the repeated display setup failure; this is an integration/TestMain
  setup failure, not a compile result or changed assertion.
- `just lint` / `golangci-lint`, `just deadcode` / `deadcode`, and
  `just vulncheck` / `govulncheck` were unavailable because the tools are not
  installed. No security-scan success is claimed.
- Fuzzing, benchmarks, profiling, generated-code checks, live display
  integration, release, Flatpak, and deployment checks were not run; this
  change has no performance or generated-code claim.

## Remaining risks

- A backend can still report an error after processing a release, so the retry
  may send a duplicate release. That is the safest available best-effort
  cleanup under an uncertain remote state; the API cannot prove remote button
  state from the error alone.
- If both the initial release and cleanup retry fail, the target may still hold
  the button; the returned error retains the primary and cleanup causes.
- No real X11, Wayland, or compositor acceptance run was possible in this
  sandbox. The regression evidence covers the shared public bundle boundary and
  fake-backend event sequence, not remote compositor state.

## Not inspected

Screen/output/clipboard internals, generic Wayland resource lifecycle, session
PID reuse, managed process cleanup, CLI privacy, Sway IPC, XTEST modifier
cleanup, window protocol implementations, integration/display servers,
release/Flatpak infrastructure, and cross-repository consumers were not
re-audited in this pass. The first four listed lifecycle/diagnostic areas were
covered by prior passes; the remaining backend and operational areas were
outside this bounded mouse/button partition.

## Repository state

- Files changed in the code commit: `bundle_input.go`, `bundle_input_test.go`.
- Code commit: `780832f` on direct `main`.
- Audit-note commit: this note's direct-main commit, recorded in the final
  handoff.
- Before code staging, the root, branch, baseline, changed-file list, and
  working-tree status were rechecked; no concurrent writer was observed.
- The staged file list contained exactly the two code files, and the code commit
  completed successfully on `main`.
- Final disposable validation artifacts were removed; no generated or debug
  files remain.

## Prompt feedback

- Productive: the repeated-pass ledger excluded the four prior partitions and
  directed the audit to an uncovered high-risk lifecycle seam.
- Productive: requiring a failing behavioral reproducer exposed that “release
  attempted” had been treated as “release completed”; static inspection alone
  would not have proved the remote-state risk.
- Productive: testing both `DoubleClick` release positions and the drag release
  caught all shared flag transitions, while the adjacent-call search confirmed
  the fix belongs in the common bundle.
- Productive: the v1.2.0 taxonomy kept focused execution, race evidence,
  compile-only success, broad sandbox socket failures, integration `TestMain`
  setup failure, and unavailable tooling separate.
- Noisy: the tagged compile-only command still executes package `TestMain`
  setup, and the display harness emitted a very large repeated Xvfb error
  stream. Future prompt guidance could recommend a package-level compile
  command that excludes integration `TestMain` when that distinction is the
  intended evidence.
- Refinement for the next pass: retain the explicit rule that lifecycle tests
  should assert event sequences and cleanup retries, not only returned errors.
