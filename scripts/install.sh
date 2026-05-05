#!/usr/bin/env sh
# Hush installer for macOS / Linux.
#
# Usage:
#   curl -fsSL https://github.com/shuakami/hush/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/shuakami/hush/releases/latest/download/install.sh | sh -s -- --version v0.1.0
#
# Env overrides:
#   HUSH_VERSION   pin a release tag (default: latest)
#   HUSH_PREFIX    install dir (default: /usr/local/bin if writable, else $HOME/.local/bin)

set -eu

REPO="shuakami/hush"
VERSION="${HUSH_VERSION:-latest}"
PREFIX="${HUSH_PREFIX:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --prefix)  PREFIX="$2";  shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

uname_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux)  echo "linux"  ;;
    darwin) echo "darwin" ;;
    *) echo "unsupported OS: $os" >&2; exit 1 ;;
  esac
}

uname_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)  echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "unsupported arch: $arch" >&2; exit 1 ;;
  esac
}

OS="$(uname_os)"
ARCH="$(uname_arch)"

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/hush_${OS}_${ARCH}.tar.gz"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/hush_${OS}_${ARCH}.tar.gz"
fi

if [ -z "$PREFIX" ]; then
  if [ -w "/usr/local/bin" ] 2>/dev/null; then
    PREFIX="/usr/local/bin"
  else
    PREFIX="$HOME/.local/bin"
  fi
fi
mkdir -p "$PREFIX"

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t hush-install)"
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading $URL"
if command -v curl >/dev/null 2>&1; then
  curl -fL --progress-bar "$URL" -o "$tmp/hush.tar.gz"
elif command -v wget >/dev/null 2>&1; then
  wget -q --show-progress "$URL" -O "$tmp/hush.tar.gz"
else
  echo "neither curl nor wget found" >&2
  exit 1
fi

echo "==> extracting"
tar -xzf "$tmp/hush.tar.gz" -C "$tmp"

echo "==> installing to $PREFIX/hush"
install -m 0755 "$tmp/hush" "$PREFIX/hush"

case ":$PATH:" in
  *":$PREFIX:"*) : ;;
  *) echo "note: $PREFIX is not on PATH; add it or move the binary somewhere on PATH." >&2 ;;
esac

echo
"$PREFIX/hush" --version 2>/dev/null || "$PREFIX/hush" version 2>/dev/null || true
echo
echo "installed: $PREFIX/hush"
echo "next:"
echo "  hush bootstrap   # initialize a local server"
echo "  hush --help"
