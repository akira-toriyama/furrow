# From-source reference formula for `brew install --HEAD`. The LIVE release
# artifact in akira-toriyama/homebrew-tap is a CASK (Casks/furrow.rb) generated
# by GoReleaser from the prebuilt binary (see .goreleaser.yaml `homebrew_casks:`)
# — GoReleaser v2 deprecated binary formulae in favor of casks. This file is not
# pushed to the tap; it documents the build and supports HEAD installs.
#
# Install (after first release): brew install akira-toriyama/tap/furrow
# From source HEAD:               brew install --HEAD akira-toriyama/tap/furrow
class Furrow < Formula
  desc "Repo-local plain-text task tracker (JSON index + per-task markdown bodies)"
  homepage "https://github.com/akira-toriyama/furrow"
  license "MIT"
  head "https://github.com/akira-toriyama/furrow.git", branch: "main"

  # Placeholder until the first release tag is published; GoReleaser fills in the
  # real url/sha256 for the binary formula it pushes to the tap.
  url "https://github.com/akira-toriyama/furrow/archive/refs/tags/v0.1.0.tar.gz"
  version "0.1.0"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X github.com/akira-toriyama/furrow/internal/version.Version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/furrow"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/furrow version")
    system bin/"furrow", "init"
    assert_predicate testpath/".furrow/index.json", :exist?
  end
end
