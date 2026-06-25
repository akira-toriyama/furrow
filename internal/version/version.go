// Package version carries the build version, overridden at release time via
// -ldflags "-X github.com/akira-toriyama/furrow/internal/version.Version=...".
// Default "dev" means a local/source build (see build.sh / .goreleaser.yaml).
package version

// Version is the furrow build version. Do not set it here; it is injected by
// the linker. "dev" is the only value a from-source build should ever show.
var Version = "dev"
