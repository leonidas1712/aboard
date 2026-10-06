# Changelog

Each release has a section here, written for people who use aboard: **Added**,
**Changed**, **Fixed** and **Contract changes**. Contract changes lists every change
under `spec/`, each saying what changed, who is affected (CLI scripts, API clients,
delivery daemons, harness adapters) and whether it is additive. The release job
publishes a version's section as its release notes. How releases are cut is in
[engineering/release.md](engineering/release.md).

## Unreleased

### Added

- Releases: signed archives for macOS and Linux on ARM and Intel with the web UI
  embedded, an SBOM per archive, and a server image on GHCR.
- The install script: `curl -fsSL https://github.com/leonidas1712/aboard/releases/latest/download/install.sh | sh`
  installs `aboard` and its launchers into `~/.local/bin` after checking the download
  against the release's signed checksums.
