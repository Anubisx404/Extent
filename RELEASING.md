# Releasing Extent

Releases are built by `.github/workflows/release.yml` when a tag matching `v*.*.*` is pushed. GoReleaser builds the archives, writes `checksums.txt`, signs it with cosign (keyless, through GitHub OIDC), and the workflow attaches GitHub build provenance attestations to each archive. Pre-release tags (for example `v1.1.0-rc.1`) are published as GitHub pre-releases automatically.

## 1. Freeze

- Announce the freeze in the project channel.
- Merge only fixes that are needed for the release. Everything else waits for the next minor.
- Confirm `main` is green in CI (`ci.yml`), including the `staticcheck` step.

## 2. Update the changelog

- Update `CHANGELOG.md` for the version being released (create it on the first release that needs one). Use Conventional Commit titles so GoReleaser groups them correctly (`feat:` under Features, `fix:` under Bug fixes, everything else under Other).
- Check that every user-visible change in the version has a note and that any deprecations follow `CONTRIBUTING.md` (Deprecation Policy).
- Check `docs/upgrading.md` covers the version's upgrade steps.

## 3. Tag the release candidate

```sh
git checkout main && git pull
git tag -a v1.1.0-rc.1 -m "Extent v1.1.0-rc.1"
git push origin v1.1.0-rc.1
```

The release workflow publishes `v1.1.0-rc.1` as a GitHub pre-release (`prerelease: auto`). Confirm that the release has `checksums.txt`, `checksums.txt.sig`, `checksums.txt.pem`, the archives, and the SBOMs.

## 4. Soak for three days

- Install the RC binary on at least one real target repository per runtime (Node, Python, Go, .NET).
- Run the live acceptance workflow (`.github/workflows/generated-acceptance.yml`) nightly against the RC for three days. Any failure stops the release: fix on `main`, cut `v1.1.0-rc.2`, and restart the soak.
- Record findings in the release tracking issue.

## 5. Tag the final release

Once the soak is clean, tag the final version from the same commit series:

```sh
git tag -a v1.1.0 -m "Extent v1.1.0"
git push origin v1.1.0
```

Confirm that the release is not marked as a pre-release and that the release notes are correct.

## 6. Verify the published artifacts

Download `checksums.txt`, `checksums.txt.sig`, `checksums.txt.pem`, and the archive you intend to check.

Verify the checksum signature:

```sh
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/Anubisx404/Extent/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Verify the archive checksum against the signed file:

```sh
sha256sum --check --ignore-missing checksums.txt
```

Verify the build provenance attestation for an archive:

```sh
gh attestation verify <archive> --repo Anubisx404/Extent
```

Run `extent version` from the extracted binary and confirm the version, commit, and date.

## 7. Announce

- Share the GitHub release link in the project channel with the release notes.
- Update the README install instructions if they reference a specific version.
- Post a short upgrade note that links to `docs/upgrading.md`.
