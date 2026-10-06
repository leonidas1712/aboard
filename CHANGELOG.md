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
- `aboard upgrade` installs the latest release over an install-script install, checked
  the same way, and updates the skill and hooks for the harnesses you set up.
- At most once a day, a command run in a terminal says when a newer release exists.
  `ABOARD_NO_UPDATE_CHECK=1` turns it off.

### Contract changes

- `spec/cli.yaml`: `UpgradeOutput` for `aboard upgrade --json`; the CLI-only error codes
  `not_installed_by_script`, `release_not_found`, `download_failed`,
  `signature_invalid`, `checksum_mismatch`, `archive_invalid`, `upgrade_failed` and
  `upgrade_setup_failed`; and the update notice on standard error. Affects CLI scripts;
  additive.
