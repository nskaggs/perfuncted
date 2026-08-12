# Release workflow

Releases are built by the GitHub Actions release workflow from a version tag.
The workflow runs the release checks, builds the Go artifacts, and attaches the
Flatpak bundle alongside the other release artifacts.

Before creating a release tag, run:

```bash
just quality
just test-integration
just test-release-static
```

CLI documentation is generated from the command sources and should be current
in the tagged tree:

```bash
just check-generate
just check-docs
just check-api-sync
```

The release workflow is defined in
[`.github/workflows/release.yml`](.github/workflows/release.yml).
