#!/bin/sh
# usage: scripts/brew-formula.sh v0.3.0 > ../homebrew-tap/Formula/lx.rb
set -eu
tag=${1:?usage: $0 vX.Y.Z}
ver=${tag#v}
base="https://github.com/iheeb1/lx/releases/download/$tag"
sums=$(curl -fsSL "$base/SHA256SUMS")
sum() { printf '%s\n' "$sums" | awk -v f="$1" '$2 == f { print $1 }'; }
for f in lx_darwin_arm64.tar.gz lx_darwin_amd64.tar.gz lx_linux_arm64.tar.gz lx_linux_amd64.tar.gz; do
	[ -n "$(sum "$f")" ] || { echo "no checksum for $f in $tag" >&2; exit 1; }
done
cat <<RUBY
class Lx < Formula
  desc "Condense command output for AI coding agents without hiding errors"
  homepage "https://github.com/iheeb1/lx"
  version "$ver"
  license "MIT"

  on_macos do
    on_arm do
      url "$base/lx_darwin_arm64.tar.gz"
      sha256 "$(sum lx_darwin_arm64.tar.gz)"
    end
    on_intel do
      url "$base/lx_darwin_amd64.tar.gz"
      sha256 "$(sum lx_darwin_amd64.tar.gz)"
    end
  end

  on_linux do
    on_arm do
      url "$base/lx_linux_arm64.tar.gz"
      sha256 "$(sum lx_linux_arm64.tar.gz)"
    end
    on_intel do
      url "$base/lx_linux_amd64.tar.gz"
      sha256 "$(sum lx_linux_amd64.tar.gz)"
    end
  end

  def install
    bin.install "lx"
  end

  def caveats
    <<~EOS
      To use lx with Claude Code, install its hook:
        lx init
      Check the setup any time with:
        lx doctor
    EOS
  end

  test do
    assert_match "lx v#{version}", shell_output("#{bin}/lx version")
    assert_equal "lx git status", shell_output("#{bin}/lx rewrite 'git status'").strip
  end
end
RUBY
