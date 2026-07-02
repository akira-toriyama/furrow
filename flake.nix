{
  # furrow — `nix run github:akira-toriyama/furrow` or `nix profile install`.
  #
  # vendorHash pins the vendored go modules; when go.mod/go.sum change, set it
  # back to pkgs.lib.fakeHash, run `nix build`, and paste the hash nix prints
  # ("got: sha256-...").
  description = "Clonable, git-native plain-text task tracker — an alternative to GitHub Projects/Issues (per-task JSON shards + markdown bodies)";

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
          vendorHash = "sha256-+NskPv31fPTL4ntWPnT5gM3QA0LoLEMXI6plccvt8aY=";
          ldflags = [
            "-s" "-w"
            "-X github.com/akira-toriyama/furrow/internal/version.Version=${version}"
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
          # go (not go_1_23): nixpkgs removed EOL go versions; go.mod's 1.23
          # floor is satisfied by any current toolchain (GOTOOLCHAIN=local).
          packages = [ pkgs.go pkgs.golangci-lint pkgs.goreleaser pkgs.git-cliff ];
        };
      });
}
