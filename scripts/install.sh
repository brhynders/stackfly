#!/usr/bin/env bash
set -euo pipefail

# --- Config ---
REPO="brhynders/stackfly"
INSTALL_DIR="/usr/local/bin"
DATA_DIR="/var/lib/stackfly"
SERVICE_NAME="stackfly"
PORT=3000

if [[ $EUID -ne 0 ]]; then
    echo "Run as root." >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive

# ════════════════════════════════════════════════════════════════════════════════
# SYSTEM HARDENING
# ════════════════════════════════════════════════════════════════════════════════

# ── Remove unused packages and services ────────────────────────────────────────

echo "==> Removing snap and unused packages..."

systemctl disable --now snapd.service snapd.socket snapd.seeded.service 2>/dev/null || true
apt-get purge -y snapd squashfs-tools gnome-software-plugin-snap 2>/dev/null || true
rm -rf /snap /var/snap /var/lib/snapd /var/cache/snapd /root/snap
cat > /etc/apt/preferences.d/no-snap.pref <<'SNAP'
Package: snapd
Pin: release a=*
Pin-Priority: -10
SNAP

REMOVE_PKGS=(
  apport whoopsie popularity-contest
  ubuntu-report motd-news-config
  lxd-agent-loader landscape-common
  cloud-init cloud-guest-utils cloud-initramfs-copymods cloud-initramfs-dyn-netconf
  open-iscsi multipath-tools mdadm
  lxd lxc lxcfs
  byobu
  openssh-server openssh-client
)

for pkg in "${REMOVE_PKGS[@]}"; do
  apt-get purge -y "$pkg" 2>/dev/null || true
done

apt-get autoremove --purge -y

# ── Harden kernel ─────────────────────────────────────────────────────────────

echo "==> Hardening kernel via sysctl..."

cat > /etc/sysctl.d/90-hardening.conf <<'SYSCTL'
# IP forwarding (required by Docker)
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1

# Ignore ICMP redirects
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv6.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0

# Ignore source-routed packets
net.ipv4.conf.all.accept_source_route = 0
net.ipv4.conf.default.accept_source_route = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv6.conf.default.accept_source_route = 0

# Enable reverse path filtering
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1

# Ignore ICMP echo broadcasts
net.ipv4.icmp_echo_ignore_broadcasts = 1

# Log martian packets
net.ipv4.conf.all.log_martians = 1
net.ipv4.conf.default.log_martians = 1

# SYN flood protection
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 2048
net.ipv4.tcp_synack_retries = 2

# Disable TCP timestamps to reduce fingerprinting
net.ipv4.tcp_timestamps = 0

# Restrict dmesg to root
kernel.dmesg_restrict = 1

# Restrict kernel pointers
kernel.kptr_restrict = 2

# Disable unprivileged BPF
kernel.unprivileged_bpf_disabled = 1

# Restrict perf_event
kernel.perf_event_paranoid = 3

# Restrict ptrace
kernel.yama.ptrace_scope = 2

# Disable core dumps
fs.suid_dumpable = 0
SYSCTL

sysctl --system

# ── Blacklist unused kernel modules ───────────────────────────────────────────

echo "==> Blacklisting unused kernel modules..."

cat > /etc/modprobe.d/blacklist-unused.conf <<'MODPROBE'
# Uncommon filesystems
install cramfs /bin/true
install freevxfs /bin/true
install hfs /bin/true
install hfsplus /bin/true
install jffs2 /bin/true
install udf /bin/true

# Uncommon network protocols
install dccp /bin/true
install sctp /bin/true
install rds /bin/true
install tipc /bin/true

# Firewire / Thunderbolt (irrelevant on VPS)
install firewire-core /bin/true
install thunderbolt /bin/true

# Bluetooth (irrelevant on VPS)
install bluetooth /bin/true
install btusb /bin/true

# USB storage (irrelevant on VPS)
install usb-storage /bin/true
MODPROBE

update-initramfs -u

# ── Update and upgrade system ─────────────────────────────────────────────────

echo "==> Updating system..."

apt-get update
apt-get dist-upgrade -y
apt-get autoremove --purge -y

# ── Automatic security updates ────────────────────────────────────────────────

echo "==> Configuring unattended-upgrades..."

apt-get install -y unattended-upgrades apt-listchanges

cat > /etc/apt/apt.conf.d/20auto-upgrades <<'AUTO'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
AUTO

cat > /etc/apt/apt.conf.d/50unattended-upgrades <<'UNATTENDED'
Unattended-Upgrade::Allowed-Origins {
    "${distro_id}:${distro_codename}";
    "${distro_id}:${distro_codename}-security";
    "${distro_id}ESMApps:${distro_codename}-apps-security";
    "${distro_id}ESM:${distro_codename}-infra-security";
    "${distro_id}:${distro_codename}-updates";
};
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:00";
UNATTENDED

systemctl enable --now unattended-upgrades

# ── Firewall ──────────────────────────────────────────────────────────────────

echo "==> Configuring UFW..."

apt-get install -y ufw

ufw default deny incoming
ufw default allow outgoing
ufw allow 80/tcp comment "HTTP"
ufw allow 443/tcp comment "HTTPS"

ufw --force enable
systemctl enable ufw

# ════════════════════════════════════════════════════════════════════════════════
# STACKFLY INSTALLATION
# ════════════════════════════════════════════════════════════════════════════════

# ── Docker ────────────────────────────────────────────────────────────────────

if ! command -v docker &>/dev/null; then
    echo "==> Installing Docker..."
    curl -fsSL https://get.docker.com | sh
fi

# ── Nixpacks ──────────────────────────────────────────────────────────────────

if ! command -v nixpacks &>/dev/null; then
    echo "==> Installing nixpacks..."
    curl -sSL https://nixpacks.com/install.sh | bash -s -- -y
fi

# ── StackFly binary ───────────────────────────────────────────────────────────

echo "==> Downloading latest StackFly release..."
DOWNLOAD_URL=$(curl -sf "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -o "https://.*stackfly-linux-amd64" | head -1)

if [ -z "$DOWNLOAD_URL" ]; then
    echo "Could not find latest release."
    if [ ! -f "${INSTALL_DIR}/stackfly" ]; then
        exit 1
    fi
    echo "Using existing binary."
else
    curl -fsSL "$DOWNLOAD_URL" -o "${INSTALL_DIR}/stackfly"
    chmod +x "${INSTALL_DIR}/stackfly"
fi

# ── Data directory ────────────────────────────────────────────────────────────

mkdir -p "$DATA_DIR"

# ── Systemd service ───────────────────────────────────────────────────────────

echo "==> Creating systemd service..."
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
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

[Install]
WantedBy=multi-user.target
EOF

echo "==> Starting StackFly..."
systemctl daemon-reload
systemctl enable ${SERVICE_NAME}
systemctl restart ${SERVICE_NAME}

sleep 3
if systemctl is-active --quiet ${SERVICE_NAME}; then
    TS_IP=$(tailscale ip -4 2>/dev/null || echo "127.0.0.1")
    echo ""
    echo "========================================="
    echo "  Installation complete."
    echo ""
    echo "  - Kernel hardened, unused modules blacklisted"
    echo "  - Auto-updates enabled (reboot at 04:00)"
    echo "  - UFW: deny incoming, allow 80/443 only"
    echo "  - OpenSSH removed (use Tailscale SSH)"
    echo "  - Docker and nixpacks installed"
    echo "  - StackFly running"
    echo ""
    echo "  Admin UI: http://${TS_IP}:${PORT}"
    echo "  Data:     ${DATA_DIR}"
    echo "  Logs:     journalctl -u stackfly -f"
    echo "========================================="
else
    echo "Failed to start. Check: journalctl -u stackfly -e"
    exit 1
fi
