# Reference copy of the Homebrew formula.
#
# The live one lives in a SEPARATE repository — olaysco/homebrew-tap — at
# Formula/itan.rb, and GoReleaser rewrites it on every release with the new
# version and checksums. This copy exists so the formula is reviewable in the
# same place as the code, and so the tap can be bootstrapped by hand.
#
# A tap is nothing more than a GitHub repo whose name starts with "homebrew-".
# `brew install olaysco/tap/itan` expands to github.com/olaysco/homebrew-tap
# and looks for Formula/itan.rb.
class Itan < Formula
  desc "Agentic AI video editor — plain-language editing over ffmpeg and a real browser"
  homepage "https://github.com/olaysco/itan"
  version "0.3.0"
  license "MIT"

  # One archive per platform. Replace the sha256 values with the ones from
  # the release's checksums.txt; `brew fetch` refuses a mismatch.
  on_macos do
    on_arm do
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_darwin_arm64.tar.gz"
      sha256 "REPLACE_WITH_CHECKSUM_darwin_arm64"
    end
    on_intel do
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_darwin_amd64.tar.gz"
      sha256 "REPLACE_WITH_CHECKSUM_darwin_amd64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_linux_arm64.tar.gz"
      sha256 "REPLACE_WITH_CHECKSUM_linux_arm64"
    end
    on_intel do
      url "https://github.com/olaysco/itan/releases/download/v#{version}/itan_#{version}_linux_amd64.tar.gz"
      sha256 "REPLACE_WITH_CHECKSUM_linux_amd64"
    end
  end

  # The dependency that makes the tap worth having: every render is ffmpeg.
  depends_on "ffmpeg"

  def install
    bin.install "itan"
  end

  # A browser cannot be a formula dependency — Chrome ships as a cask, and
  # most machines already have one — so it is stated rather than installed.
  def caveats
    <<~EOS
      itan needs a Chromium-family browser for `compose` — Chrome, Chromium,
      Edge, or Brave. Most machines already have one; if not:

        brew install --cask google-chrome

      Then check everything at once:

        itan doctor
    EOS
  end

  test do
    assert_match "itan", shell_output("#{bin}/itan version")
    system bin/"itan", "help"
  end
end
