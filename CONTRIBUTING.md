# Contributing

## Prerequisites

- Go 1.27.0
- [`just`](https://github.com/casey/just)
- A Linux desktop session for local runtime checks

Install the repository's development tools with:

```bash
just install-dev-tools
```

The integration suite additionally needs the display, Wayland, X11, D-Bus,
clipboard, and test-application packages listed in
[the CI workflow](.github/workflows/ci.yml).

## Checks

Use the fast pre-commit gate while iterating:

```bash
just precommit
```

Before submitting a change, run the full static and unit suite:

```bash
just quality
```

Run integration checks sequentially because they share managed display and
temporary-session resources:

```bash
just test-integration
```

For CLI changes, regenerate and verify the checked-in reference:

```bash
just docs
just check-api-sync
```

Keep generated files formatted, preserve the public API contract, and include
tests for behavior changes.
