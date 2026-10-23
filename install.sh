#!/bin/sh
# Install hqdb from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/Snarap1/hqdb/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Snarap1/hqdb/main/install.sh | sh -s -- --version v1.0.0 --dir /usr/local/bin
#
# Environment overrides:
#   HQDB_VERSION         release tag to install (default: the latest release)
#   HQDB_INSTALL_DIR     install directory (default: $HOME/.local/bin)
#   HQDB_DOWNLOAD_BASE   release asset base URL (default: the GitHub releases download URL; used by tests)
set -eu

REPO="${HQDB_REPO:-Snarap1/hqdb}"
BINARY="hqdb"
TAG="${HQDB_VERSION:-}"
DIR="${HQDB_INSTALL_DIR:-$HOME/.local/bin}"

usage() {
	cat <<'EOF'
usage: install.sh [--version <tag>] [--dir <path>]

Install hqdb from a GitHub release:

  curl -fsSL https://raw.githubusercontent.com/Snarap1/hqdb/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/Snarap1/hqdb/main/install.sh | sh -s -- --version v1.0.0 --dir /usr/local/bin

Environment overrides:
  HQDB_VERSION         release tag to install (default: the latest release)
  HQDB_INSTALL_DIR     install directory (default: $HOME/.local/bin)
  HQDB_DOWNLOAD_BASE   release asset base URL (default: the GitHub releases download URL; used by tests)
  HQDB_REPO            owner/name of the GitHub repo to install from (default: Snarap1/hqdb)
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	-v | --version)
		TAG="$2"
		shift 2
		;;
	-d | --dir)
		DIR="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'install.sh: unknown argument: %s\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

die() {
	printf 'hqdb install: %s\n' "$1" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
MINGW* | MSYS* | CYGWIN*) os=windows ;;
*) die "unsupported OS: $(uname -s); build from source instead: go install github.com/$REPO@latest" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7l | armv7) arch=arm ;;
*) die "unsupported architecture: $(uname -m); build from source instead: go install github.com/$REPO@latest" ;;
esac

fetch() {
	url="$1"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$url"
	else
		die "neither curl nor wget is available"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/.*= *//'
	else
		die "no sha256 tool (sha256sum, shasum, openssl) found; cannot verify the download"
	fi
}

# Asset names carry no version, so an untagged install can use the
# releases/latest/download alias and never has to resolve the latest tag.
if [ -n "${HQDB_DOWNLOAD_BASE:-}" ]; then
	base="${HQDB_DOWNLOAD_BASE%/}"
elif [ -n "$TAG" ]; then
	base="https://github.com/$REPO/releases/download/$TAG"
else
	base="https://github.com/$REPO/releases/latest/download"
fi

if [ "$os" = "windows" ]; then
	member="$BINARY.exe"
	ext="zip"
else
	member="$BINARY"
	ext="tar.gz"
fi
asset="${BINARY}_${os}_${arch}.${ext}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

printf 'hqdb install: downloading from %s (%s/%s)\n' "$base" "$os" "$arch"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"
have=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt" | head -1)
[ -n "$have" ] || die "no checksum for $asset in checksums.txt"

want=
attempt=1
while :; do
	fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
	want=$(sha256_of "$tmp/$asset")
	if [ "$want" = "$have" ]; then break; fi
	# A release whose assets were re-uploaded can be served stale from the CDN
	# for a few seconds, so one re-download is worth it before giving up.
	if [ "$attempt" -ge 2 ]; then break; fi
	printf 'hqdb install: checksum mismatch for %s, downloading again\n' "$asset" >&2
	attempt=$((attempt + 1))
done
[ "$want" = "$have" ] || die "checksum mismatch for $asset: expected $have, downloaded $want"

if [ "$ext" = "zip" ]; then
	# Windows Git-Bash ships bsdtar as tar.exe and usually unzip; GNU tar cannot read zip.
	if command -v bsdtar >/dev/null 2>&1; then
		bsdtar -xf "$tmp/$asset" -C "$tmp" "$member" || die "cannot unpack $asset"
	elif command -v unzip >/dev/null 2>&1; then
		unzip -qo "$tmp/$asset" "$member" -d "$tmp" || die "cannot unpack $asset"
	else
		die "bsdtar or unzip is needed to unpack $asset"
	fi
else
	tar -xzf "$tmp/$asset" -C "$tmp" "$member" || die "cannot unpack $asset"
fi
mkdir -p "$DIR"

# Replace the installed binary atomically, so a running hqdb keeps working.
target="$DIR/$member"
cp "$tmp/$member" "$target.tmp.$$"
chmod +x "$target.tmp.$$"
mv -f "$target.tmp.$$" "$target"
printf 'hqdb install: installed %s\n' "$target"

case ":${PATH:-}:" in
*":$DIR:"*) ;;
*) printf 'hqdb install: %s is not on PATH; add it to your shell profile\n' "$DIR" ;;
esac

printf 'hqdb install: next: run hqdb check, then open the terminal UI with hqdb\n'
printf 'hqdb install: for an AI agent: claude mcp add hqdb -- %s mcp\n' "$target"
