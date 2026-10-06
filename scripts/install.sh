#!/bin/sh
# Installs aboard from a GitHub release: the aboard binary and the launchers shipped
# with it (aboard-launcher-<name>), for macOS or Linux on ARM or Intel.
#
#   curl -fsSL https://github.com/leonidas1712/aboard/releases/latest/download/install.sh | sh
#
# Steps:
#
#   1. Pick the archive for this system: aboard_<version>_<os>_<arch>.tar.gz.
#   2. Download it, the release's checksums.txt and the checksums' signature bundle
#      (checksums.txt.sigstore.json) into a temporary folder.
#   3. When cosign is installed, check the bundle: the checksums must be signed by this
#      repository's release workflow (.github/workflows/release.yml) running on the
#      version's tag, with a certificate from GitHub's OIDC issuer. Without cosign, say
#      how to check it by hand.
#   4. Check the archive's SHA-256 against checksums.txt.
#   5. Refuse an archive holding anything but plain files at its top level (no folders,
#      links or paths), then unpack it and run the new aboard once (aboard version).
#   6. Copy each program into the install folder under a temporary name and rename it
#      into place, so an interrupted install never leaves a half-written aboard.
#
# Any failure stops before step 6, and an aboard already installed stays as it was.
# Nothing runs with sudo.
#
# Environment:
#
#   ABOARD_VERSION       the version to install, such as 0.2.0 (default: the latest
#                        release)
#   ABOARD_INSTALL_DIR   the folder to install into (default: ~/.local/bin)
#   ABOARD_DOWNLOAD_URL  where releases are downloaded from (default:
#                        https://github.com/leonidas1712/aboard/releases); https only,
#                        except for a server on this machine
#
# Exit codes: 0 installed, 1 failed (the message says why and what to do).

set -eu

repo=leonidas1712/aboard
identity_prefix="https://github.com/$repo/.github/workflows/release.yml@refs/tags/v"
issuer=https://token.actions.githubusercontent.com

say() { printf '%s\n' "$*"; }
fail() {
	printf 'aboard install: %s\n' "$1" >&2
	exit 1
}

tmp=""
cleanup() { if [ -n "$tmp" ]; then rm -rf "$tmp"; fi; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

# The system.
case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "aboard has builds for macOS and Linux only, not $(uname -s). Build it from source: https://github.com/$repo#quick-start" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "aboard has builds for arm64 and amd64 only, not $(uname -m)." ;;
esac
# A shell running under Rosetta on an Apple silicon Mac reports x86_64; install the
# native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
	arch=arm64
fi

# Where to download from.
base=${ABOARD_DOWNLOAD_URL:-https://github.com/$repo/releases}
base=${base%/}
case "$base" in
https://*) proto=https ;;
http://127.0.0.1:* | http://localhost:* | http://127.0.0.1/* | http://localhost/*) proto=http ;;
*) fail "ABOARD_DOWNLOAD_URL must start with https:// (got $base)." ;;
esac

if command -v curl >/dev/null 2>&1; then
	download() { curl -fsSL --proto "=$proto" --proto-redir "=$proto" --retry 2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	download() { wget -q -O "$2" "$1"; }
else
	fail "downloading needs curl or wget; install one and run this again."
fi
fetch() {
	download "$1" "$2" || fail "couldn't download $1. Check your connection and run this again."
}

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	fail "checking the download needs sha256sum or shasum; install one and run this again."
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t aboard-install)

# The version, and the archive's name in checksums.txt.
version=${ABOARD_VERSION:-}
version=${version#v}
if [ -z "$version" ]; then
	fetch "$base/latest/download/checksums.txt" "$tmp/latest.txt"
	version=$(awk -v suffix="_${os}_${arch}.tar.gz" '
		{ n = $2; if (substr(n, 1, 7) == "aboard_" && substr(n, length(n) - length(suffix) + 1) == suffix) print substr(n, 8, length(n) - 7 - length(suffix)) }
	' "$tmp/latest.txt" | head -n 1)
	[ -n "$version" ] || fail "the latest release has no build for $os/$arch."
fi
case "$version" in
*[!0-9A-Za-z.+-]* | "") fail "ABOARD_VERSION must be a version such as 0.2.0 (got $version)." ;;
esac
archive="aboard_${version}_${os}_${arch}.tar.gz"
release="$base/download/v$version"

say "Downloading aboard $version for $os/$arch"
fetch "$release/checksums.txt" "$tmp/checksums.txt"
fetch "$release/checksums.txt.sigstore.json" "$tmp/checksums.txt.sigstore.json"
fetch "$release/$archive" "$tmp/$archive"

# The signature on the checksums.
identity="$identity_prefix$version"
if command -v cosign >/dev/null 2>&1; then
	if ! cosign verify-blob --bundle "$tmp/checksums.txt.sigstore.json" \
		--certificate-identity "$identity" --certificate-oidc-issuer "$issuer" \
		"$tmp/checksums.txt" >"$tmp/cosign.log" 2>&1; then
		cat "$tmp/cosign.log" >&2
		fail "the checksums' signature doesn't check out: they weren't signed by aboard's release workflow for v$version. Nothing was installed."
	fi
	say "Checked the signature: signed by aboard's release workflow for v$version"
else
	say "cosign isn't installed, so the checksums' signature wasn't checked. To check it, install cosign and run:"
	say "  cosign verify-blob --bundle checksums.txt.sigstore.json --certificate-identity $identity --certificate-oidc-issuer $issuer checksums.txt"
fi

# The archive's checksum.
want=$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")
[ "$(printf '%s\n' "$want" | grep -c .)" = 1 ] || fail "checksums.txt for v$version has no single entry for $archive. Nothing was installed."
got=$(sha256 "$tmp/$archive")
[ "$got" = "$want" ] || fail "$archive doesn't match its checksum (got $got, want $want): the download is incomplete or was changed. Nothing was installed; run this again."

# The archive's contents: plain files with plain names only.
tar -tzf "$tmp/$archive" >"$tmp/names" 2>/dev/null || fail "$archive isn't a readable archive. Nothing was installed."
tar -tvzf "$tmp/$archive" >"$tmp/entries" 2>/dev/null || fail "$archive isn't a readable archive. Nothing was installed."
if grep -v -E '^[A-Za-z0-9_][A-Za-z0-9._-]*$' "$tmp/names" >/dev/null || grep -v '^-' "$tmp/entries" >/dev/null; then
	fail "$archive holds something other than plain files (a folder, a link or a path). Nothing was installed."
fi
mkdir "$tmp/unpacked"
tar -xzf "$tmp/$archive" -C "$tmp/unpacked" || fail "couldn't unpack $archive. Nothing was installed."
new="$tmp/unpacked/aboard"
if [ ! -f "$new" ] || [ -L "$new" ]; then
	fail "$archive has no aboard program. Nothing was installed."
fi
chmod 755 "$tmp"/unpacked/aboard*
"$new" version >/dev/null 2>&1 || fail "the downloaded aboard doesn't run on this system. Nothing was installed."

# Install: each program goes in under a temporary name, then is renamed into place.
dir=${ABOARD_INSTALL_DIR:-$HOME/.local/bin}
mkdir -p "$dir" || fail "couldn't create $dir. Set ABOARD_INSTALL_DIR to a folder you can write to."
for f in "$tmp"/unpacked/aboard "$tmp"/unpacked/aboard-launcher-*; do
	[ -f "$f" ] || continue
	name=$(basename "$f")
	part="$dir/.$name.install.$$"
	if ! cp "$f" "$part" || ! chmod 755 "$part" || ! mv -f "$part" "$dir/$name"; then
		rm -f "$part"
		fail "couldn't write $dir/$name. Set ABOARD_INSTALL_DIR to a folder you can write to."
	fi
	say "Installed $dir/$name"
done

case ":$PATH:" in
*":$dir:"*) next="aboard init" ;;
*)
	say ""
	say "$dir isn't on your PATH. Add it, for example in ~/.profile or ~/.zshrc:"
	say "  export PATH=\"$dir:\$PATH\""
	next="$dir/aboard init"
	;;
esac
say ""
say "aboard $version is installed. Next, set up your harnesses: $next"
