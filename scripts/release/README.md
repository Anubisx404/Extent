# Release verification

Run `bash scripts/release/dry-run.sh` from the repository root. It validates the
GoReleaser configuration and performs a snapshot build with archives, SHA-256
checksums, and SPDX JSON SBOMs. The script never publishes or creates a GitHub
release.

The script uses locally installed GoReleaser and Syft when both are available.
Otherwise, it uses the pinned `goreleaser/goreleaser:v2.12.7` Docker image,
which includes Syft.

The release workflow runs only for `v*.*.*` tags and publishes the release after
tests, vetting, formatting, and module-tidiness checks pass. Tagging and
publishing are intentionally outside local verification.
