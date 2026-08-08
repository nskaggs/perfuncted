# Perfuncted v1 migration notes

Perfuncted v1 deliberately removes weakly typed and implementation-only APIs.

## Downstream migration status

The in-scope downstream migrations are implemented in Snatchblock and
Listingwatch:

1. Each repository owns its private `internal/contextutil` helper.
2. All imports use the local helper; the removed public `perfuncted/ctxutil`
   package is not retained as an alias.
3. Removed error and lifecycle symbols are no longer referenced.
4. Browser lifecycle code uses the canonical `*perfuncted.Session`; caller-
   supplied sessions are borrowed, while factory-created sessions are owned
   and closed by the managed browser.
5. No module-local `replace` directives or workspace-only compatibility APIs
   were added.

The remaining release-boundary check is to compile each downstream module with
`GOWORK=off` after an unreleased v1 candidate is published. The current module
files intentionally remain pinned to the latest released versions because this
work does not create or publish releases; the workspace `go.work` overlay is
development validation only.
