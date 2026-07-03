#!/bin/sh
# Install the latest smolanalytics release binary.
#   curl -fsSL https://raw.githubusercontent.com/Arjun0606/smolanalytics/main/install.sh | sh
set -e

REPO="Arjun0606/smolanalytics"
BIN="smolanalytics"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64 | amd64) ARCH=amd64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [ -z "$TAG" ]; then echo "no release found yet — try: go install github.com/$REPO/cmd/smolanalytics@latest" >&2; exit 1; fi

URL="https://github.com/$REPO/releases/download/$TAG/${BIN}_${OS}_${ARCH}.tar.gz"
echo "downloading $BIN $TAG ($OS/$ARCH)..."
TMP=$(mktemp -d)
curl -fsSL "$URL" | tar -xz -C "$TMP"

# prefer /usr/local/bin; fall back to ~/.local/bin without sudo (CI, coding
# agents, users without root). PREFIX overrides everything.
DEST="${PREFIX:-/usr/local/bin}"
if [ -w "$DEST" ]; then
  mv "$TMP/$BIN" "$DEST/$BIN"
elif [ -z "${PREFIX:-}" ] && [ -t 0 ] && command -v sudo >/dev/null 2>&1; then
  echo "installing to $DEST needs sudo (or re-run with PREFIX=\$HOME/.local/bin)"
  sudo mv "$TMP/$BIN" "$DEST/$BIN"
else
  DEST="$HOME/.local/bin"
  mkdir -p "$DEST"
  mv "$TMP/$BIN" "$DEST/$BIN"
  case ":$PATH:" in
    *":$DEST:"*) ;;
    *) echo "note: $DEST is not on your PATH — add:  export PATH=\"$DEST:\$PATH\"" ;;
  esac
fi
rm -rf "$TMP"

echo "installed $BIN -> $DEST/$BIN"
echo "try:  $BIN demo"
