# Releasing f9s

Releases are built by GitHub Actions and GoReleaser when a `v*` tag is pushed.
Archives, checksums, SBOMs, and a keyless Cosign signature are attached to the
GitHub release. GoReleaser also updates the Homebrew tap.

## One-time public-launch checks

- The `f9s` repository is public and its default branch is protected.
- `imankhadiv/homebrew-tap` is public.
- The `TAP_GITHUB_TOKEN` repository secret can write to the tap and has the
  smallest practical scope.
- Private vulnerability reporting is enabled in the repository security
  settings.
- The license, security policy, contribution guide, and issue forms are visible
  from the public repository.

## Prepare a release

1. Confirm the default branch is green on Linux, macOS, and Windows.
2. Review user-facing changes and breaking changes since the previous tag.
3. Validate the release definition:

   ```sh
   goreleaser check
   ```

4. If GoReleaser, Cosign, and Syft are installed locally, build a non-publishing
   snapshot and inspect `dist/`:

   ```sh
   goreleaser release --snapshot --clean
   ```

5. Create and push an annotated semantic-version tag from the reviewed commit:

   ```sh
   git tag -a v0.1.0 -m "f9s v0.1.0"
   git push origin v0.1.0
   ```

Pushing the tag publishes externally. Check the tag and commit carefully before
that final command.

## Verify the published release

- All Linux, macOS, and Windows archives are present.
- `checksums.txt`, its signature and certificate, and the SBOM artifacts are
  attached.
- A downloaded binary prints the expected version with `f9s --version`.
- The Homebrew formula was updated and `brew install imankhadiv/tap/f9s`
  succeeds on a clean machine.
- Release notes accurately separate features, fixes, and other changes.

If publishing fails, fix the workflow or credentials and rerun the failed job.
Do not move an existing public tag to a different commit; create a new patch
version when released artifacts need replacement.
