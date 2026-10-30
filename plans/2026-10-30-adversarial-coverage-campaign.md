# Perfuncted adversarial coverage campaign — 2026-10-30

## Measurement and contract

Baseline: `79e794f211b847fbac7d793282eb50f78d841432` on `main`.
`GOWORK=off CGO_ENABLED=0 go test -short -count=1 -covermode=atomic -coverprofile=/tmp/perfuncted-cover.out ./...` measured **64.5%** aggregate statements (19,215 statements).
The first implemented pass passes the entire short suite and measures **69.5%**, an increase of **5.0 percentage points**. This is a statement coverage measurement, not a measure of live authorization or full acceptance.

Each subsequent pass must aim for at least five further percentage points of **aggregate** coverage while proving each consequential defect with a failing regression before fixing it. Once fewer than five percentage points remain, close the entire remaining gap. Do not claim 100% because some packages show 100% while others remain low.

## First pass: behavior and defect evidence

Added private D-Bus and real Unix-socket synthetic Remote Desktop/EIS handshake tests: grant request/denial, version, session, capability binding, EIS handshake, resumed device setup, key/text/button/pointer/scroll event protocol, cancellation, device revocation, held-key/button release, persistent-token refusal and malformed messages. Also tested synthetic screenshot portal denial, invalid response type, URI/capture/hash and cancellation, and CLI multi-monitor fail-closed behavior after simulated hotplug.

Regression tests found and fixes addressed:
- EIS string parsing accepted embedded NULs.
- Screenshot portal accepted malformed authorization status types as successful responses.
- Wayland shared-memory fallback left a temporary file after a failed truncate.
- Benchmark parser accepted infinite sample metrics and an invalid non-finite regression budget.
- CLI window watch surfaced normal cancellation during enumeration as an error.
- Test input/screenshot fixtures ignored canceled contexts.
- Real headless Wayland Pluma clipboard-paste-to-save failed intermittently. Added post-paste visual settling before saving; five consecutive isolated replays and one complete headless Wayland suite passed.

Generated the previously stale CLI docs and updated the CLI/API composite-command mapping. Added direct tests for the API verification checker.

## Next passes: risk-first, not line-count-first

1. **Pass 2 target: 74.5%** — additional portal/EIS failure-phase integration and device protocol tests, screen capture/Wayland operation failures, and actual XTest/uinput observable events, including scroll and drag.
2. **Pass 3 target: 79.5%** — multi-monitor coordinate transforms (mixed scales, negative origins, rotation, hotplug mid-operation), and clear fail-closed contracts when topology cannot be proved.
3. **Pass 4 target: 84.5%** — real GNOME Shell bridge lifecycle/restart and compositor-backed input/capture behavior.
4. **Pass 5 target: 89.5%** — real Firefox/KDE/GTK chooser completion (both X11 and Wayland), accessibility events and backend failure handling.
5. **Pass 6 target: 94.5%** — remaining package and CLI failure paths, ownership/cleanup and cancellation races.
6. **Pass 7 target: 100%** — final uncovered statements and behavior-first acceptance, avoiding meaningless tests just to execute lines.

These are targets rather than proven future results. Real user consent and mixed desktop environments cannot be inferred from mock results. Refer to `DESKTOP_INPUT_ACCEPTANCE.md` for complete live acceptance gaps and limitations.

## Verification commands

```sh
GOWORK=off CGO_ENABLED=0 go test -short -count=1 -covermode=atomic -coverprofile=/tmp/perfuncted-coverage.out ./...
GOWORK=off go tool cover -func=/tmp/perfuncted-coverage.out | tail -1
GOWORK=off CGO_ENABLED=1 go test -race -short -count=1 ./...
PATH=$HOME/go/bin:$PATH just precommit
PATH=$HOME/go/bin:$PATH golangci-lint run --new-from-rev=HEAD ./...
GOWORK=off CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=integration ./integration -run '^TestIntegration$' -count=1
```
