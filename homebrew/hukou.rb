# Homebrew formula for hukou.
#
# Live in the rtwsvj/hukou tap (brew tap rtwsvj/hukou) and submitted to
# homebrew/homebrew-core (PR #299130). The four sha256 values are the
# published v0.5.0 release checksums.
class Hukou < Formula
  desc "Adopt, upgrade, and roll back CLI tools no package manager owns"
  homepage "https://github.com/rtwsvj/hukou"
  version "0.5.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.5.0/hukou_0.5.0_darwin_arm64.tar.gz"
      sha256 "af23d64a07817aedef77fdb690a34f2ea4a9643c34329ab59508399611fb1a5d"
    end
    on_intel do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.5.0/hukou_0.5.0_darwin_amd64.tar.gz"
      sha256 "06b0bfe1afcf5644226a7689bd0724e53eb673bc4bc92fb6eac2070928c40207"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.5.0/hukou_0.5.0_linux_arm64.tar.gz"
      sha256 "36387fd62ddef244db72b8d4e907be7eb60ee5f1c750b72d11659e3823676295"
    end
    on_intel do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.5.0/hukou_0.5.0_linux_amd64.tar.gz"
      sha256 "48ee544f611c1d2a1181265932404aecfa706257e60e3edccd5e8c6703094f0a"
    end
  end

  def install
    bin.install "hukou"
    prefix.install "LICENSE", "LICENSES", "README.md"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/hukou version")
  end
end
