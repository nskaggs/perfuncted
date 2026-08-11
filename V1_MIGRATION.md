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

Perfuncted `v1.0.0` is published. Snatchblock `v0.6.3` and Listingwatch
`v0.1.2` consume that final version through fetchable module requirements and
both have passed their `GOWORK=off` validation. The workspace `go.work` overlay
is development convenience only and is not release evidence.
