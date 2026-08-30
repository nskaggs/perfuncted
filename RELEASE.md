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

## Release notes

Use [`release/RELEASE_NOTES_TEMPLATE.md`](release/RELEASE_NOTES_TEMPLATE.md)
as the default format for every release:

- Open with a one- or two-sentence summary.
- Group the details by theme under short headings.
- Use dry, factual bullets for user-visible changes and validation.
- Do not repeat the project or version header in the release body.
- Omit commit counts and extended narrative; the comparison link provides the
  complete commit list.
