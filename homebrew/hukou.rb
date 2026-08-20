# Homebrew formula for hukou.
#
# Live in the rtwsvj/hukou tap (brew tap rtwsvj/hukou) and submitted to
# homebrew/homebrew-core (PR #299130). The four sha256 values are the
# published v0.4.0 release checksums.
class Hukou < Formula
  desc "Adopt, upgrade, and roll back CLI tools no package manager owns"
  homepage "https://github.com/rtwsvj/hukou"
  version "0.4.0"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.4.0/hukou_0.4.0_darwin_arm64.tar.gz"
      sha256 "ba3515519f820f57c24d175f085461f918ae8480adfa6539a036ac10b7301f53"
    end
    on_intel do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.4.0/hukou_0.4.0_darwin_amd64.tar.gz"
      sha256 "f2444a03cda155be1fe34d87c2bd54a21eceb8b8d48cb3f644e2384e194e5c4e"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.4.0/hukou_0.4.0_linux_arm64.tar.gz"
      sha256 "dd1f1fa9eea055b8d19644cee3cf6c67c3e0cf675677a36ce7d052473b14edaf"
    end
    on_intel do
      url "https://github.com/rtwsvj/hukou/releases/download/v0.4.0/hukou_0.4.0_linux_amd64.tar.gz"
      sha256 "419ad0e36a075eea8373342e099ca762e40234f7a7fcecd7d3e4257e00d51155"
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
