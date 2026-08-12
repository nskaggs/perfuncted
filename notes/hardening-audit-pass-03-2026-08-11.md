# Perfuncted hardening audit pass 3/10

- Repository: `/home/nskaggs/workspace/perfuncted`
- Prompt: `/home/nskaggs/workspace/codex/prompts/code-hardening-audit.md` v1.1.0
- Baseline: `8146c6e2361d88026a1c206e5ca5b993d63d2eab` on `main`
- Code-fix commit: `db98143` (`fix: validate Sway IPC responses`)
- Audit-note commit: recorded in the final handoff after this note is committed

## Git and prior coverage

- The target started clean on `main`; `git status --porcelain` was empty.
- The parent-workspace invocation was intentionally rooted to the nested target;
  `git rev-parse --show-toplevel` returned this repository.
- No applicable repository-local instruction files existed under `.agents/` or
  `.codex/`; the README and `justfile` supplied the build/test guidance.
- Pass 1 and pass 2 notes were consulted. Pass 1 covered session lifecycle,
  managed applications, process groups, window ownership, and wait invalidation;
  pass 2 covered CLI diagnostics and inherited-environment disclosure. Their
  fixes (`a042831`, `292930a`) and those partitions were not re-audited.
- Recent backend hardening history consulted included `1ced9dc`, `71ccec6`,
  `ed4285d`, and the prior capture/input/window fixes. The selected scope was
  the Sway IPC transport/protocol boundary, which pass 2 explicitly left open.

## Git-write preflight

Before source edits, the following passed against the nested target:

- `git rev-parse --show-toplevel` returned
  `/home/nskaggs/workspace/perfuncted`; branch was `main`; the index lock was
  absent; and the initial porcelain output was empty.
- A temporary index under `.git` was read from `HEAD`, written to a tree equal
  to `HEAD^{tree}`, used to stage a disposable virtual probe, reset to `HEAD`,
  and written again to the identical tree.
- `git update-ref refs/heads/main HEAD HEAD` passed without changing the ref.
- `git commit --dry-run --allow-empty --no-verify -m 'audit preflight: no content'`
  returned the expected clean-tree result: `nothing to commit, working tree
  clean` (exit 1 because there was no content to commit).

## Scope and hypotheses

This pass inspected Sway's length-prefixed IPC framing, request/response type
matching, command acknowledgement parsing, persistent query path, and the
dedicated window-event subscription handshake. The primary hypotheses were:

1. A short nil-error write could be accepted as a complete IPC request.
2. A response for a different request type could be consumed as the requested
   response, desynchronizing protocol state or accepting the wrong payload.
3. An empty or partially failed command acknowledgement could be reported as
   success.

The adjacent subscription path was checked after the main fix because it used
the same framing but bypassed the query helper.

## Findings and fixes

### H-001 — medium severity, high confidence: short IPC writes were false success

- Paths: `window/sway.go`, `window/sway_context_test.go`.
- Behavior: `writeSwayMessage` discarded the byte count and returned nil when a
  connection returned fewer bytes with no error.
- Impact: a truncated header or payload could be treated as a sent request,
  causing protocol desynchronization or an operation whose result no longer
  corresponds to the caller's request.
- Evidence: `TestWriteSwayMessageRejectsShortWrite` uses a deterministic
  `net.Conn` test double returning one byte and nil; it failed on the baseline
  with `error = <nil>`.
- Fix: check the byte count and return `io.ErrShortWrite`. This is the lowest
  shared framing layer and covers query and subscription sends.

### H-002 — medium severity, high confidence: response message types were not validated

- Paths: `window/sway.go`, `window/sway_context_test.go`,
  `window/sway_event_test.go`.
- Behavior: query and subscription code accepted any well-framed message type
  as the response to the outstanding request.
- Impact: a wrong or out-of-order response could be interpreted as a tree or
  command result, hiding protocol desynchronization and producing false state.
- Evidence: `TestSwayQueryConnRejectsUnexpectedResponseType` and
  `TestSwayWindowChangesRejectsUnexpectedSubscriptionResponse` send a valid
  frame with the wrong type; both enforce rejection at the socket boundary.
- Fix: add shared `readSwayResponse`, require the expected type for queries and
  subscription acknowledgement, and correct the existing reflow test fixture
  to send protocol-accurate response types.

### H-003 — medium severity, high confidence: command acknowledgements had false-success paths

- Paths: `window/sway.go`, `window/sway_context_test.go`.
- Behavior: `swayCmd` returned nil for an empty result array and checked only
  the first result when multiple command results were returned.
- Impact: callers could believe a mutating window operation succeeded when no
  command was acknowledged or a later command failed.
- Evidence: the table-driven `TestSwayCmdRequiresSuccessfulCommandResults`
  failed on the baseline for both `[]` and
  `[{"success":true},{"success":false,...}]`.
- Fix: reject empty result arrays and require every returned command result to
  report success at the shared command acknowledgement parser.

## Validation

Passed:

- `gofmt -w window/sway.go window/sway_context_test.go window/sway_event_test.go`
- `git diff --check` and `git diff --cached --check`
- Baseline reproducers first failed as expected after the test additions:
  `GOWORK=off CGO_ENABLED=0 go test -p=1 -vet=off ./window -run
  'Test(WriteSwayMessageRejectsShortWrite|SwayQueryConnRejectsUnexpectedResponseType|SwayCmdRequiresSuccessfulCommandResults)$'
  -count=1`.
- Focused fixed tests passed with workspace-local disposable Go storage:
  `GOCACHE=/home/nskaggs/workspace/perfuncted/.audit-cache
  GOTMPDIR=/home/nskaggs/workspace/perfuncted/.audit-tmp GOWORK=off
  CGO_ENABLED=0 go test -p=1 -vet=off ./window -run
  'Test(WriteSwayMessageRejectsShortWrite|SwayQueryConnRejectsUnexpectedResponseType|SwayCmdRequiresSuccessfulCommandResults)$'
  -count=1`.
- Sway unit subset passed:
  `GOWORK=off CGO_ENABLED=0 go test -p=1 -vet=off ./window -run
  '^TestSway|^TestWriteSwayMessage' -count=1`.
- Full `window` short unit tests passed with `go test -p=1 -vet=off
  -short -count=1 ./window`.
- Full `window` race tests passed with `go test -race -p=1 -vet=off -short
  -count=1 ./window`; the focused Sway race subset also passed.
- `go vet ./window` and `go vet -p=1 ./...` passed.
- Compile-only untagged packages passed with `go test -p=1 -vet=off -run '^$'
  ./...`; `GOWORK=off go mod verify` passed with `all modules verified`.
- The disconfirming search of all Sway framing call sites found query and
  subscription paths using the shared write helper, query response validator,
  and the intended raw event reader.
- No performance or generated-code change was made, so benchmark, profiling,
  fuzz, and generation claims are not made.

Environment/resource failures and gaps:

- The first attempted focused run used `/tmp/perfuncted-audit3-tmp` before
  creating it, so it failed at environment setup. After creation, `/tmp` then
  failed with `disk quota exceeded` while compiling; this was not a code test
  result. Workspace-local disposable storage produced the valid receipts above.
- `go test -p=1 -vet=off -short -count=1 ./...` failed only in unrelated
  socket-backed tests with sandbox `setsockopt: operation not permitted`
  listener failures in `cmd/pf`, `input`, and `internal/wl`; the changed
  `window` package passed.
- `go test -tags=integration -run '^$' ./...` was not a clean compile-only
  receipt because integration `TestMain` setup still attempted Xvfb and failed
  to bind `/tmp/.X11-unix`; this is a sandbox/Xvfb ownership failure, not a
  Sway source failure.
- `staticcheck`, `golangci-lint`, `govulncheck`, and `deadcode` were not
  installed in the environment, so those checks were unavailable.

## Remaining risks and rejected hypotheses

- No real Sway compositor/socket integration run was possible in this sandbox;
  the acceptance evidence is socket-level with `net.Pipe` and package tests.
- The pass did not change the 64 MiB Sway body limit, JSON tree resource bounds,
  or direct post-`Close` behavior of the exported manager; those are separate
  hypotheses requiring a new bounded scope.
- No additional Sway defect was promoted after the call-site disconfirmation;
  the pass stopped at the natural boundary requested by the checkpoint.

## Not inspected

Screen, input, output, clipboard, generic Wayland protocol internals, session
PID reuse, managed process cleanup, CLI diagnostics, integration/display
servers, release/Flatpak infrastructure, and cross-repository consumers were
not inspected in this pass. The first five lifecycle/diagnostic areas were
covered or explicitly excluded by prior passes; the remaining backend and
operational areas were intentionally left for later passes rather than
broadening this one.

## Prompt feedback

- Productive: the repeated-pass ledger excluded the process-group and CLI
  disclosure partitions and directed the audit to an uncovered protocol seam.
- Productive: requiring failing socket-boundary reproducers exposed three
  concrete false-success/integrity defects and also revealed an inaccurate
  existing test fixture that sent the wrong response type.
- Productive: requiring an adjacent-caller check caught the subscription path
  that would otherwise have bypassed response validation.
- Noisy/environment-sensitive: `/tmp` quota and sandbox listener/Xvfb failures
  made broad receipts unusable; workspace-local caches and explicit failure
  classification kept code evidence separate from host limitations.
- Refinement: the prompt could explicitly distinguish compile-only commands
  from packages whose `TestMain` executes display setup even with `-run '^$'`.
