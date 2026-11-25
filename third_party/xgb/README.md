# XGB maintenance

The authoritative inputs are upstream.json (exact upstream Go module version,
origin commit and checksums), and the ordered patches in patches/. The
generated internal/third_party/xgb tree is committed so library consumers need
no generation step, nested Go module, or replace directive.

- just xgb-check: download the pinned archive, verify checksums, normalize Go
  import paths and remove the nested go.mod, apply patches in order, and compare
  every generated file with the committed tree. Required by just quality (CI
  and release).
- just xgb-sync: regenerate the tree after reviewing changes to the pin or
  patches. The tool builds in scratch before swapping the generated directory.
- just xgb-update v1.3.1: read-only candidate check with a new exact version.
  Reports the first conflicting patch or prints candidate pin metadata. Review
  upstream changes, update the manifest/patches manually, regenerate, and test.
  A clean patch application is not proof of behavioral compatibility.

001 contains the interdependent XI2 framing, context-aware connection and
abort/backpressure changes, with their regression tests. 002 contains the
main-module vet cleanup, opt-in upstream live-X11 test, and local change notes.
Import relocation is mechanical in the Go sync tool, not a huge patch.
The upstream license is preserved byte for byte. Do not publish upstream
changes, create a fork, or edit generated source as the source of truth.
