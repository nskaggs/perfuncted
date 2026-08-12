# Perfuncted Improvement and Right-Sizing Plan

## Scope and current contract

Perfuncted v1 is a released Linux desktop-automation library and CLI. The
public surface is frozen as recorded in `V1_API_AUDIT.md`; improvements should
prefer correctness, lifecycle safety, diagnostics, and bounded resource use
over new backend breadth or compatibility aliases.

The inherited commit `9bd9a8d` closes a PID-reuse hazard in stale-session
cleanup by checking a recorded child process's `XDG_RUNTIME_DIR` before
signalling its process group. Its focused lifecycle tests pass locally with
the host toolchain, one worker, and the parent workspace disabled.

## Priority decisions

| Priority | Item | Decision and completion evidence |
| --- | --- | --- |
| High | Never terminate a reused or unrelated recorded PID | Completed by `9bd9a8d`; `/proc/<pid>/environ` must identify the stale runtime before its process group is stopped. |
| High | Reap owned children that ignore graceful termination | Implemented: recheck runtime ownership after the SIGTERM grace period before escalating the still-owned process group to SIGKILL. |
| High | Remove the unsafe manual cleanup bypass | Implemented: `just cleanup-nested` now calls `pf session cleanup`, which delegates to `CleanupStaleSessions` instead of scanning all processes, sending unconditional SIGKILL, and recursively deleting every matching path. |
| Medium | Make cleanup usable and testable from the CLI | Implemented: `pf session cleanup --max-age` enforces the library's five-minute creation grace, emits a completion summary, and accepts an injected cleaner in unit tests so tests never touch real runtime directories. |
| Medium | Keep CLI docs synchronized | Generate the new command reference and retain `check-generate` as the drift gate. |
| Medium | Keep validation isolated and small | Use `GOWORK=off`, `GOTOOLCHAIN=local`, `GOMAXPROCS=1`, `go test -p 1`, and focused packages before considering the full suite. |
| Medium | State toolchain policy accurately | Local just recipes pin the validated patch release; CI reads the supported Go line from `go.mod`. |
| Medium | Describe cleanup timing precisely | Document that dead owners are immediate, missing metadata keeps the creation grace, and `--max-age` governs malformed owner metadata. |
| Medium | Bound unsafe library cleanup ages | Implemented: incomplete or malformed ownership metadata is retained for at least five minutes even when a library caller passes zero or a negative duration. |

## Deferred or rejected expansion

- Do not weaken the v1 freeze with compatibility aliases, duplicate context
  helpers, or backend implementation types in the root API.
- Do not add a force-clean mode that bypasses process ownership checks. Manual
  investigation is safer when corrupt metadata prevents automatic cleanup.
- Do not run compositor, Flatpak, race, or full integration matrices on the
  constrained host during routine review. Those jobs require explicit
  resource preparation and should remain serialized even then.
- Do not clear module, compiler, or compositor caches as part of cleanup.
  Session cleanup owns only Perfuncted runtime directories and their verified
  child processes.
- Do not add cleanup result types to the frozen public API solely for richer
  CLI output; the current completion message is sufficient without widening
  the library contract.

## Validation ladder

1. Run `gofmt` only on changed Go files and `git diff --check`.
2. Run the inherited stale-ownership tests in the root package.
3. Run focused CLI cleanup and documentation tests in `./cmd/pf`.
4. Regenerate CLI Markdown and verify that only expected command references
   change.
5. Run non-display unit packages with one worker when memory permits.
6. Leave nested display, race, release, and Flatpak suites to CI or a prepared
   local resource window.

## Adversarial review checklist

- Confirm cleanup tests inject a fake cleaner and cannot glob or remove real
  runtime directories.
- Confirm ages below the five-minute session-creation grace cannot reach the
  cleanup implementation.
- Confirm reused child PIDs with a mismatched runtime remain alive.
- Confirm an owned recorded child that ignores SIGTERM is escalated only after
  its runtime ownership is rechecked.
- Confirm zero or negative ages cannot bypass the five-minute metadata grace.
- Confirm generated help describes ownership checks without promising that
  every corrupt directory will be removed.
- Search maintenance recipes for any remaining wildcard process kills or
  broad `perfuncted-xdg-*` deletion.
