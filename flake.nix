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
  # go.sum sha256: 74d87f808d27b33c22ac1f0d15b5fc21e8105178e14d085d730662ec4cb32483
  description = "Clonable, git-native plain-text task tracker — an alternative to GitHub Projects/Issues (per-task JSON shards + markdown bodies)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        # Lockstep with the release tag: a flake has no tag info at eval time, so
        # this is bumped in release-prep alongside sync-task-status.yml's
        # furrow-version default (scripts/check-version-lockstep.sh enforces the
        # match), so `nix run/install` never reports a stale version (audit F9).
        version = "0.10.0";
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
          vendorHash = "sha256-E3G7TfEfsgaBWSA+iSN0loxmB3weB5zTjuy6QUKtTu4=";
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
