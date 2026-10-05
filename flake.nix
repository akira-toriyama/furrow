{
  # furrow — `nix run github:akira-toriyama/furrow` or `nix profile install`.
  #
  # vendorHash pins the vendored go modules; when go.mod/go.sum change, set it
  # back to pkgs.lib.fakeHash, run `nix build`, paste the hash nix prints
  # ("got: sha256-..."), and refresh the stamp below to the new go.sum's sha256.
  # The stamp is what lets CI catch a forgotten re-pin without running nix
  # (scripts/check-version-lockstep.sh compares it against the real go.sum —
  # #127 changed go.sum without re-pinning, and every nix build after it failed
  # on a hash mismatch until the audit that added this guard noticed).
  #
  # go.sum sha256: df531e75e3a5011cd6440a33dd76c9323d1854c0a5f293949ac4db3bfcf3f374
  description = "Clonable, git-native plain-text task tracker — an alternative to GitHub Projects/Issues (per-task JSON shards + markdown bodies)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        # THE release pin: a flake has no tag info at eval time, so this is the one
        # version literal release-prep bumps. scripts/check-version-lockstep.sh
        # holds the pushed tag to it at release time and check-readme-parity.sh
        # holds README's pin to it, so `nix run/install` never reports a stale
        # version (audit F9).
        version = "8.0.0-rc.1";
        # The nix store src has no .git, so version.Resolve's VCS-stamp fallback
        # finds nothing; stamp Commit explicitly from the flake's own revision
        # (dirtyRev when the tree is uncommitted) so `furrow version` isn't blank.
        rev = self.rev or self.dirtyRev or "unknown";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "furrow";
          inherit version;
          src = ./.;
          vendorHash = "sha256-GRQ9gX/ayoQowSQV37jNokUbRvR4FS/o2dwLrbYiXEU=";
          ldflags = [
            "-s" "-w"
            "-X github.com/akira-toriyama/furrow/internal/version.Version=${version}"
            "-X github.com/akira-toriyama/furrow/internal/version.Commit=${rev}"
          ];
          subPackages = [ "cmd/furrow" ];
          meta = with pkgs.lib; {
            description = "Clonable, git-native plain-text task tracker — an alternative to GitHub Projects/Issues";
            homepage = "https://github.com/akira-toriyama/furrow";
            license = licenses.mit;
            mainProgram = "furrow";
          };
        };

        apps.default = flake-utils.lib.mkApp {
          drv = self.packages.${system}.default;
          name = "furrow";
        };

        devShells.default = pkgs.mkShell {
          # go (not a pinned go_1_xx): nixpkgs removed EOL go versions; go.mod's
          # 1.25.0 floor is satisfied by any current toolchain (GOTOOLCHAIN=local).
          packages = [ pkgs.go pkgs.golangci-lint pkgs.goreleaser pkgs.git-cliff ];
        };
      });
}
