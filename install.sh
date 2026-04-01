#!/usr/bin/env bash
set -euo pipefail

REPO="dgzlopes/tulip"
BIN="tulip"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64 | amd64) ARCH="amd64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# Resolve version
if [ -z "${VERSION:-}" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
fi

ASSET="${BIN}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

DEST="${INSTALL_DIR}/${BIN}"
if [ -x "$DEST" ]; then
  CURRENT="$("$DEST" --version 2>/dev/null || echo "unknown")"
  echo "Upgrading ${BIN} ${CURRENT} → ${VERSION} (${OS}/${ARCH})..."
else
  echo "Installing ${BIN} ${VERSION} (${OS}/${ARCH})..."
fi

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

curl -fsSL "$URL" -o "$TMP"
chmod +x "$TMP"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "$DEST"
else
  sudo mv "$TMP" "$DEST"
fi

echo "Done: ${DEST}"
