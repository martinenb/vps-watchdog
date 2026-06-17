#!/usr/bin/env bash
set -euo pipefail

# VPS Watchdog install script

MIN_GO_VERSION="1.22"

check_go_version() {
    if ! command -v go &>/dev/null; then
        return 1
    fi
    local version
    version=$(go version | awk '{print $3}' | sed 's/go//')
    local major minor
    major=$(echo "$version" | cut -d. -f1)
    minor=$(echo "$version" | cut -d. -f2)
    local min_major min_minor
    min_major=$(echo "$MIN_GO_VERSION" | cut -d. -f1)
    min_minor=$(echo "$MIN_GO_VERSION" | cut -d. -f2)
    if [ "$major" -gt "$min_major" ] || ([ "$major" -eq "$min_major" ] && [ "$minor" -ge "$min_minor" ]); then
        return 0
    fi
    return 1
}

install_go() {
    local arch
    arch=$(uname -m)
    local goarch
    case $arch in
        x86_64) goarch="amd64" ;;
        aarch64|arm64) goarch="arm64" ;;
        *) echo "Unsupported architecture: $arch"; exit 1 ;;
    esac

    # Fetch latest stable Go version dynamically
    echo "Fetching latest Go version..."
    local goversion
    goversion=$(curl -fsSL "https://go.dev/VERSION?m=text" 2>/dev/null | head -1)
    if [ -z "$goversion" ]; then
        goversion="go1.23.4"  # fallback
    fi
    echo "Installing ${goversion}..."
    local tarball="${goversion}.linux-${goarch}.tar.gz"
    local url="https://go.dev/dl/${tarball}"
    echo "Downloading $url ..."
    curl -fsSL "$url" -o "/tmp/${tarball}"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${tarball}"
    rm "/tmp/${tarball}"
    export PATH="/usr/local/go/bin:$PATH"
    echo "Go installed: $(go version)"
}

echo "=== VPS Watchdog Installer ==="

# Check / install Go
if ! check_go_version; then
    echo "Go >= ${MIN_GO_VERSION} not found. Installing..."
    install_go
else
    echo "Go found: $(go version)"
fi

# Build
echo "Building vps-watchdog..."
cd "$(dirname "$0")"
go mod download
go build -ldflags="-s -w" -o /usr/local/bin/vps-watchdog ./cmd/watchdog/
echo "Binary installed to /usr/local/bin/vps-watchdog"

# Create directories
echo "Creating directories..."
mkdir -p /var/log/watchdog
mkdir -p /var/lib/watchdog
mkdir -p /etc/watchdog

# Copy config if not exists
if [ ! -f /etc/watchdog/config.toml ]; then
    cp config.toml /etc/watchdog/config.toml
    echo "Config installed to /etc/watchdog/config.toml"
    echo "NOTE: Edit /etc/watchdog/config.toml and set your Brevo API key and email addresses."
else
    echo "Config already exists at /etc/watchdog/config.toml, skipping."
fi

# Install systemd unit
if command -v systemctl &>/dev/null; then
    cp systemd/vps-watchdog.service /etc/systemd/system/vps-watchdog.service
    systemctl daemon-reload
    systemctl enable --now vps-watchdog
    echo "Systemd service enabled and started."
    echo ""
    MY_IP=$(hostname -I | awk '{print $1}')
    echo "======================================================"
    echo "Watchdog running!"
    echo "Web UI at http://${MY_IP}:47832"
    echo "Logs: journalctl -u vps-watchdog -f"
    echo "======================================================"
else
    echo "systemctl not found. Start manually with:"
    echo "  /usr/local/bin/vps-watchdog --config /etc/watchdog/config.toml"
fi
