#!/bin/bash
set -e

# --- Config (change this to your repo) ---
REPO="your-username/stackfly"

INSTALL_DIR="/usr/local/bin"
DATA_DIR="/var/lib/stackfly"
SERVICE_NAME="stackfly"
PORT=3000

# --- Checks ---

if [ "$(id -u)" -ne 0 ]; then
    echo "Run as root (or with sudo)"
    exit 1
fi

if ! command -v docker &>/dev/null; then
    echo "Installing Docker..."
    curl -fsSL https://get.docker.com | sh
fi

if ! command -v nixpacks &>/dev/null; then
    echo "Installing nixpacks..."
    curl -sSL https://nixpacks.com/install.sh | bash -s -- -y
fi

if ! command -v tailscale &>/dev/null; then
    echo "Warning: Tailscale not found. StackFly will bind to 127.0.0.1."
    echo "Install Tailscale for remote access: https://tailscale.com/download"
fi

# --- Download binary ---

echo "Downloading latest StackFly release..."
DOWNLOAD_URL=$(curl -sf "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -o "https://.*stackfly-linux-amd64" | head -1)

if [ -z "$DOWNLOAD_URL" ]; then
    echo "Could not find latest release. Check REPO variable or place binary manually."
    if [ ! -f "${INSTALL_DIR}/stackfly" ]; then
        exit 1
    fi
    echo "Using existing binary."
else
    curl -fsSL "$DOWNLOAD_URL" -o "${INSTALL_DIR}/stackfly"
    chmod +x "${INSTALL_DIR}/stackfly"
    echo "Installed: $(${INSTALL_DIR}/stackfly --version 2>/dev/null || echo 'ok')"
fi

# --- Create data directory ---

mkdir -p "$DATA_DIR"

# --- Create systemd service ---

echo "Creating systemd service..."
cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=StackFly PaaS
After=network.target docker.service tailscaled.service
Requires=docker.service
Wants=tailscaled.service

[Service]
Type=simple
ExecStartPre=/bin/sleep 2
ExecStart=${INSTALL_DIR}/stackfly --data-dir ${DATA_DIR} --port ${PORT}
Restart=always
RestartSec=5
Environment=HOME=/root

[Install]
WantedBy=multi-user.target
EOF

# --- Start ---

echo "Starting StackFly..."
systemctl daemon-reload
systemctl enable ${SERVICE_NAME}
systemctl restart ${SERVICE_NAME}

sleep 3
if systemctl is-active --quiet ${SERVICE_NAME}; then
    TS_IP=$(tailscale ip -4 2>/dev/null || echo "127.0.0.1")
    echo ""
    echo "========================================="
    echo " StackFly is running"
    echo " Admin UI: http://${TS_IP}:${PORT}"
    echo " Data:     ${DATA_DIR}"
    echo " Logs:     journalctl -u stackfly -f"
    echo "========================================="
else
    echo "Failed to start. Check: journalctl -u stackfly -e"
    exit 1
fi
