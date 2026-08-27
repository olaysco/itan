# Publishing itan to a Homebrew tap

A tap is just a GitHub repository whose name begins with `homebrew-`.
`brew install olaysco/tap/itan` expands to `github.com/olaysco/homebrew-tap`
and looks for `Formula/itan.rb`. There is no registry, no review, no waiting
— which is why a tap is the right first distribution channel, and why
`homebrew-core` (which requires notability and forbids pre-built binaries
without an audit) is not.

Once set up, releasing is `git tag` and nothing else.

## One-time setup

**1. Create the tap repository.** Public, empty, named exactly
`homebrew-tap` under the `olaysco` account. The `homebrew-` prefix is what
makes `olaysco/tap` resolve.

**2. Create a token for it.** The release workflow runs in the `itan` repo,
and its built-in `GITHUB_TOKEN` cannot write to a different repository. Make
a fine-grained personal access token scoped to `olaysco/homebrew-tap` only,
with `Contents: read and write`. Add it to the `itan` repo as the secret
`HOMEBREW_TAP_TOKEN` (Settings → Secrets and variables → Actions).

**3. Turn on the workflow.**

```bash
git mv .github/release.yml.pending .github/workflows/release.yml
```

Pushing a workflow file needs a token with `workflow` scope, which is why it
ships disabled.

## Releasing

```bash
goreleaser check                        # config valid for your version
goreleaser release --snapshot --clean   # full dry run, publishes nothing

git tag -a v0.3.0 -m "v0.3.0"
git push origin v0.3.0
```

The tag triggers the workflow, which runs the tests, cross-compiles six
binaries, publishes a GitHub Release with `checksums.txt`, and commits an
updated `Formula/itan.rb` to the tap with the new version and checksums.

Verify:

```bash
brew untap olaysco/tap 2>/dev/null
brew install olaysco/tap/itan
itan version
itan doctor
```

## Doing it by hand instead

GoReleaser is a convenience, not a requirement. To bootstrap the tap without
it: copy `docs/tap/itan.rb` into the tap repo at `Formula/itan.rb`, build and
upload the archives yourself, and replace each `REPLACE_WITH_CHECKSUM_*` with
the matching value from `checksums.txt` (or `shasum -a 256 <file>`). Homebrew
refuses a download whose hash does not match, so a stale checksum fails loudly
rather than installing the wrong bytes.

## Why ffmpeg is a dependency and a browser is not

`depends_on "ffmpeg"` is most of the reason to ship a tap: it removes the one
prerequisite that otherwise makes a first run fail. A browser cannot be
declared the same way — Chrome is a cask, and casks are not valid formula
dependencies — so it is named in `caveats` instead, alongside the
`brew install --cask google-chrome` line for anyone who needs it.

`itan doctor` remains the honest check either way.
