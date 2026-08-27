# Publishing itan to a Homebrew tap

A tap is just a GitHub repository whose name begins with `homebrew-`.
`brew install olaysco/tap/itan` expands to `github.com/olaysco/homebrew-tap`
and looks for `Casks/itan.rb`. There is no registry, no review, no waiting
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
updated `Casks/itan.rb` to the tap with the new version and checksums.

Verify:

```bash
brew untap olaysco/tap 2>/dev/null
brew install olaysco/tap/itan
itan version
itan doctor
```

## Doing it by hand instead

GoReleaser is a convenience, not a requirement. To bootstrap the tap without
it: copy `docs/tap/itan.rb` into the tap repo at `Casks/itan.rb`, build and
upload the archives yourself, and replace each `REPLACE_WITH_CHECKSUM_*` with
the matching value from `checksums.txt` (or `shasum -a 256 <file>`). Homebrew
refuses a download whose hash does not match, so a stale checksum fails loudly
rather than installing the wrong bytes.

## Cask, not formula

GoReleaser's `brews` block is deprecated — soft since v2.10, hard since v2.16.
Formulae are meant to build from source; the ones that shipped pre-compiled
binaries were always a workaround, kept alive because Linuxbrew had no
alternative. It does now, so `homebrew_casks` is the supported path. Nothing
changes for the person installing: `brew install olaysco/tap/itan` is the same
command either way.

Two consequences worth knowing:

- Casks **must** live in `Casks/`. That is the default, and goreleaser rejects
  any other directory, so the config sets none.
- itan is not code-signed or notarized, so macOS quarantines the download and
  the first run dies with a Gatekeeper error that explains nothing. The cask
  strips the attribute in a `postflight` hook. If you ever sign and notarize
  the binaries, that hook can go.

We never published a formula, so there is nothing to migrate. If a formula had
already shipped, the tap would also need a `tap_migrations.json` mapping the
old name to the new one, and the cask would want `conflicts_with` the formula.

## Why ffmpeg is a dependency and a browser is not

`depends_on formula: "ffmpeg"` is most of the reason to ship a tap: it removes the one
prerequisite that otherwise makes a first run fail. A browser cannot be
declared the same way — Chrome is itself a cask, and most machines already
have a browser — so it is named in `caveats` instead, alongside the
`brew install --cask google-chrome` line for anyone who needs it.

`itan doctor` remains the honest check either way.
