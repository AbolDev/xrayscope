#!/usr/bin/env bash
# XrayScope installer / updater / remover.
#
#   bash install.sh                       install or update the panel next to 3x-ui
#   bash install.sh agent --server URL --token TOKEN [--name NAME]
#                                         install as a node agent
#   bash install.sh uninstall [--purge]   remove the service (and data with --purge)
#
# Environment:
#   XRAYSCOPE_VERSION   release tag to install (default: latest)
#   XRAYSCOPE_REPO      GitHub repository "owner/name"
#   XRAYSCOPE_LISTEN    panel address (default 0.0.0.0:8686)
#   XRAYSCOPE_BIN       use this local binary instead of downloading
set -euo pipefail

REPO="${XRAYSCOPE_REPO:-AbolDev/xrayscope}"
INSTALL_DIR=/usr/local/bin
CONF_DIR=/etc/xrayscope
DATA_DIR=/var/lib/xrayscope
UNIT=/etc/systemd/system/xrayscope.service

red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
info()  { printf '\033[36m==>\033[0m %s\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { red "Run this script as root (sudo)."; exit 1; }
command -v systemctl >/dev/null || { red "systemd is required."; exit 1; }

ACTION=install
case "${1:-}" in
  agent|uninstall) ACTION="$1"; shift ;;
esac

SERVER_URL=""; TOKEN=""; NODE_NAME=""; PURGE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER_URL="$2"; shift 2 ;;
    --token)  TOKEN="$2"; shift 2 ;;
    --name)   NODE_NAME="$2"; shift 2 ;;
    --purge)  PURGE=1; shift ;;
    *) red "unknown option: $1"; exit 2 ;;
  esac
done

if [ "$ACTION" = uninstall ]; then
  info "Stopping and removing the service"
  systemctl disable --now xrayscope 2>/dev/null || true
  rm -f "$UNIT" "$INSTALL_DIR/xrayscope"
  systemctl daemon-reload
  if [ "$PURGE" -eq 1 ]; then rm -rf "$CONF_DIR" "$DATA_DIR"; green "Removed, including data."; else green "Removed. Data kept in $DATA_DIR and $CONF_DIR."; fi
  exit 0
fi

arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) red "unsupported CPU: $(uname -m)"; exit 1 ;;
  esac
}

download() {
  local ver="${XRAYSCOPE_VERSION:-latest}" a url tmp
  a="$(arch)"
  if [ "$ver" = latest ]; then
    url="https://github.com/$REPO/releases/latest/download/xrayscope-linux-$a.tar.gz"
  else
    url="https://github.com/$REPO/releases/download/$ver/xrayscope-linux-$a.tar.gz"
  fi
  tmp="$(mktemp -d)"
  info "Downloading $url"
  curl -fsSL "$url" -o "$tmp/x.tgz" || { red "Download failed. Check XRAYSCOPE_REPO / XRAYSCOPE_VERSION."; exit 1; }
  if curl -fsSL "$url.sha256" -o "$tmp/x.sha" 2>/dev/null; then
    (cd "$tmp" && sed 's#  .*#  x.tgz#' x.sha | sha256sum -c - >/dev/null) || { red "Checksum mismatch."; exit 1; }
  fi
  tar -xzf "$tmp/x.tgz" -C "$tmp"
  install -m 0755 "$tmp/xrayscope" "$INSTALL_DIR/xrayscope"
  rm -rf "$tmp"
}

if [ -n "${XRAYSCOPE_BIN:-}" ]; then
  info "Installing local binary $XRAYSCOPE_BIN"
  install -m 0755 "$XRAYSCOPE_BIN" "$INSTALL_DIR/xrayscope"
else
  download
fi

mkdir -p "$CONF_DIR" "$DATA_DIR"
chmod 700 "$DATA_DIR"

if [ "$ACTION" = agent ]; then
  [ -n "$SERVER_URL" ] && [ -n "$TOKEN" ] || { red "agent needs --server and --token"; exit 2; }
  PANEL_DB=/etc/x-ui/x-ui.db
  PANEL_TYPE=3x-ui
  [ -f "$PANEL_DB" ] || PANEL_TYPE=none
  "$INSTALL_DIR/xrayscope" init -f -o "$CONF_DIR/config.json" -role agent -server "$SERVER_URL" -token "$TOKEN" \
    -name "${NODE_NAME:-$(hostname)}" -data "$DATA_DIR" -panel "$PANEL_TYPE" -panel-db "$PANEL_DB"
  chmod 600 "$CONF_DIR/config.json"
else
  if [ ! -f "$CONF_DIR/config.json" ]; then
    PANEL_TYPE=3x-ui
    [ -f /etc/x-ui/x-ui.db ] || PANEL_TYPE=none
    "$INSTALL_DIR/xrayscope" init -o "$CONF_DIR/config.json" -listen "${XRAYSCOPE_LISTEN:-0.0.0.0:8686}" \
      -data "$DATA_DIR" -panel "$PANEL_TYPE" -panel-db /etc/x-ui/x-ui.db
    chmod 600 "$CONF_DIR/config.json"
  else
    info "Keeping the existing configuration $CONF_DIR/config.json"
  fi
fi

info "Installing the systemd service"
cat > "$UNIT" <<EOF
[Unit]
Description=XrayScope traffic analytics
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/xrayscope serve -c $CONF_DIR/config.json
Restart=always
RestartSec=3
LimitNOFILE=65536
# the panel database and the Xray log are read-only for us
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$DATA_DIR /etc/x-ui

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable xrayscope >/dev/null 2>&1
systemctl restart xrayscope
sleep 2

if ! systemctl is-active --quiet xrayscope; then
  red "The service did not start. Logs:"; journalctl -u xrayscope -n 30 --no-pager; exit 1
fi

green "XrayScope is running."
echo
"$INSTALL_DIR/xrayscope" doctor -c "$CONF_DIR/config.json" || true
echo
if [ "$ACTION" = agent ]; then
  echo "This node now reports to $SERVER_URL."
else
  IP="$(curl -fsS4 --max-time 4 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')"
  PORT="$(grep -o '"listen": *"[^"]*"' "$CONF_DIR/config.json" | sed 's/.*:\([0-9]*\)"/\1/')"
  echo "Open the panel:  http://${IP}:${PORT:-8686}/"
  if [ -f "$DATA_DIR/initial-credentials.txt" ]; then
    echo; echo "First login:"; cat "$DATA_DIR/initial-credentials.txt"
  else
    echo "Reset the admin password any time with:  xrayscope passwd -c $CONF_DIR/config.json"
  fi
  echo
  echo "Put the panel behind HTTPS (reverse proxy or tls_cert/tls_key in the config) before exposing it publicly."
fi
