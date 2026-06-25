{
  # furrow — `nix run github:akira-toriyama/furrow` or `nix profile install`.
  #
  # NOTE: nix is not installed on the author's machine (MEMO §8), so vendorHash
  # below is a placeholder (lib.fakeHash). On the first build nix will print the
  # correct hash ("got: sha256-...") — paste it in. CI / another machine will
  # finalize this; from-source `go install` and Homebrew work today regardless.
  description = "Repo-local plain-text task tracker (JSON index + per-task markdown bodies)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = "0.1.0-dev";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "furrow";
          inherit version;
          src = ./.;
          vendorHash = pkgs.lib.fakeHash; # TODO: replace with the hash nix prints
          ldflags = [
            "-s" "-w"
            "-X github.com/akira-toriyama/furrow/internal/version.Version=${version}"
          ];
          subPackages = [ "cmd/furrow" ];
          meta = with pkgs.lib; {
            description = "Repo-local plain-text task tracker";
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
          packages = [ pkgs.go_1_23 pkgs.golangci-lint pkgs.goreleaser pkgs.git-cliff ];
        };
      });
}
