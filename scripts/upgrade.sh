#!/bin/bash
set -e

REPO="brhynders/stackfly"
INSTALL_DIR="/usr/local/bin"

if [ "$(id -u)" -ne 0 ]; then
    echo "Run as root (or with sudo)"
    exit 1
fi

echo "Downloading latest StackFly release..."
DOWNLOAD_URL=$(curl -sf "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -o "https://.*stackfly-linux-amd64" | head -1)

if [ -z "$DOWNLOAD_URL" ]; then
    echo "Could not find latest release."
    exit 1
fi

curl -fsSL "$DOWNLOAD_URL" -o "${INSTALL_DIR}/stackfly"
chmod +x "${INSTALL_DIR}/stackfly"

echo "Restarting StackFly..."
systemctl restart stackfly

sleep 2
if systemctl is-active --quiet stackfly; then
    echo "Upgrade complete."
else
    echo "Failed to start. Check: journalctl -u stackfly -e"
    exit 1
fi
