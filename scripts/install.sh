#!/usr/bin/env bash
# Installs the latest screenlet-player release for this machine's OS/arch.
#
#   curl -fsSL https://raw.githubusercontent.com/BirdRa1n/screenlet-player/main/scripts/install.sh | bash
#
# This is the same logic the future player.screenlet.app/install.sh will
# redirect to — see docs/ROADMAP.md.
set -euo pipefail

REPO="BirdRa1n/screenlet-player"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="screenlet-player"

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) echo "error: unsupported OS '$os'" >&2; exit 1 ;;
esac

case "$arch" in
  x86_64|amd64)  goarch="amd64" ;;
  aarch64|arm64) goarch="arm64" ;;
  armv7l|armv7)  goarch="armv7" ;;
  *) echo "error: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

if [ "$goos" = "darwin" ] && [ "$goarch" != "arm64" ]; then
  echo "error: darwin builds are only published for arm64 (dev use only — not a signage target)" >&2
  exit 1
fi

asset="screenlet-player-${goos}-${goarch}"
echo "==> Detected platform: ${goos}-${goarch}"

api_url="https://api.github.com/repos/${REPO}/releases/latest"
release_json="$(curl -fsSL "$api_url")"

download_url="$(printf '%s' "$release_json" \
  | grep -o "\"browser_download_url\": *\"[^\"]*${asset}\"" \
  | head -n1 \
  | sed -E 's/.*"(https:[^"]+)"/\1/')"

if [ -z "$download_url" ]; then
  echo "error: no release asset found for ${asset}" >&2
  echo "check https://github.com/${REPO}/releases for available builds" >&2
  exit 1
fi

tag="$(printf '%s' "$release_json" | grep -o '"tag_name": *"[^"]*"' | head -n1 | sed -E 's/.*"([^"]+)"$/\1/')"
echo "==> Installing ${tag} from ${download_url}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

curl -fsSL "$download_url" -o "${tmp_dir}/${asset}"
curl -fsSL "${download_url}.sha256" -o "${tmp_dir}/${asset}.sha256" 2>/dev/null || true

if [ -s "${tmp_dir}/${asset}.sha256" ]; then
  echo "==> Verifying checksum"
  ( cd "$tmp_dir" && sha256sum -c "${asset}.sha256" )
fi

chmod +x "${tmp_dir}/${asset}"

if [ -w "$INSTALL_DIR" ]; then
  mv "${tmp_dir}/${asset}" "${INSTALL_DIR}/${BIN_NAME}"
else
  echo "==> ${INSTALL_DIR} is not writable, retrying with sudo"
  sudo mv "${tmp_dir}/${asset}" "${INSTALL_DIR}/${BIN_NAME}"
fi

echo "==> Installed ${BIN_NAME} ${tag} to ${INSTALL_DIR}/${BIN_NAME}"
"${INSTALL_DIR}/${BIN_NAME}" -version || true
