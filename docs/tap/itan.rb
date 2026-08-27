# Reference copy of the Homebrew CASK.
#
# The live one lives in a SEPARATE repository — olaysco/homebrew-tap — at
# Casks/itan.rb, and GoReleaser rewrites it on every release with the new
# version and checksums. This copy exists so the cask is reviewable next to
# the code, and so the tap can be bootstrapped by hand.
#
# A tap is nothing more than a GitHub repo whose name starts with "homebrew-".
# `brew install olaysco/tap/itan` expands to github.com/olaysco/homebrew-tap.
#
# Cask, not formula: a formula is meant to build from source, and the ones
# that shipped pre-built binaries were always a workaround. GoReleaser
# deprecated that path in v2.10 and removed it in v2.16.
cask "itan" do
  version "0.3.0"

  # Replace each sha256 with the value from the release's checksums.txt.
  # Homebrew refuses a download whose hash does not match, so a stale
  # checksum fails loudly rather than installing the wrong bytes.
  on_macos do
    on_arm do
      sha256 "REPLACE_WITH_CHECKSUM_darwin_arm64"
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_darwin_arm64.tar.gz",
          verified: "github.com/olaysco/itan"
      binary "itan"
    end
    on_intel do
      sha256 "REPLACE_WITH_CHECKSUM_darwin_amd64"
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_darwin_amd64.tar.gz",
          verified: "github.com/olaysco/itan"
      binary "itan"
    end
  end

  on_linux do
    on_arm do
      sha256 "REPLACE_WITH_CHECKSUM_linux_arm64"
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_linux_arm64.tar.gz",
          verified: "github.com/olaysco/itan"
      binary "itan"
    end
    on_intel do
      sha256 "REPLACE_WITH_CHECKSUM_linux_amd64"
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_linux_amd64.tar.gz",
          verified: "github.com/olaysco/itan"
      binary "itan"
    end
  end

  name "itan"
  desc "Agentic AI video editor — plain-language editing over ffmpeg and a real browser"
  homepage "https://github.com/olaysco/itan"

  livecheck do
    skip "Auto-generated on release."
  end

  # The dependency that makes the tap worth having: every render is ffmpeg.
  depends_on formula: "ffmpeg"

  # The binary is neither code-signed nor notarized, so macOS quarantines it
  # and the first run dies with a Gatekeeper error that explains nothing.
  postflight do
    if OS.mac?
      system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/itan"]
    end
  end

  caveats <<~EOS
    itan needs a Chromium-family browser for `compose` — Chrome, Chromium,
    Edge, or Brave. Most machines already have one; if not:

      brew install --cask google-chrome

    Then check everything at once:

      itan doctor
  EOS
end
