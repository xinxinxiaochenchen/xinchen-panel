#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: sh install-vps.sh [--public-preview]

Download or update xinchen-panel and deploy its WebUI with Docker Compose.
Requires Git, Docker Engine, and the Docker Compose plugin. Go and Node.js
are built inside Docker and do not need to be installed on the host.

Default: bind HTTP to 127.0.0.1:18080 for your HTTPS reverse proxy.
--public-preview: expose a read-only HTTP preview on 0.0.0.0:18080.

Installation directory: /opt/xinchen-panel
Override with XINCHEN_PANEL_DIR=/absolute/path (must be writable).
Run the same command again to update a clean main branch checkout.
EOF
}

case "${1:-}" in
  ''|--public-preview) ;;
  --help|-h) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

repo=https://github.com/xinxinxiaochenchen/xinchen-panel.git
install_dir=${XINCHEN_PANEL_DIR:-/opt/xinchen-panel}
case "$install_dir" in
  /*) ;;
  *) echo 'XINCHEN_PANEL_DIR must be an absolute path.' >&2; exit 1 ;;
esac
if [ "$install_dir" = / ] || [ -L "$install_dir" ]; then
  echo 'The installation directory cannot be / or a symbolic link.' >&2
  exit 1
fi
if ! command -v git >/dev/null 2>&1; then
  echo 'Git is required. Install Git, then run this installer again.' >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo 'Docker Engine and the Compose plugin must be installed and usable by this account.' >&2
  echo 'Install Docker from https://docs.docker.com/engine/install/ and run this installer again.' >&2
  exit 1
fi

if [ -e "$install_dir" ]; then
  if [ ! -d "$install_dir" ] || [ ! -e "$install_dir/.git" ]; then
    echo "The installation directory is not a checkout: $install_dir. Choose another XINCHEN_PANEL_DIR." >&2
    exit 1
  fi
  checkout_root=$(git -C "$install_dir" rev-parse --show-toplevel)
  resolved_dir=$(CDPATH= cd -- "$install_dir" && pwd -P)
  if [ "$checkout_root" != "$resolved_dir" ]; then
    echo 'The installation directory is not the root of a checkout.' >&2
    exit 1
  fi
  origin=$(git -C "$install_dir" remote get-url origin)
  case "$origin" in
    "$repo"|git@github.com:xinxinxiaochenchen/xinchen-panel.git) ;;
    *) echo 'The checkout belongs to a different repository; refusing to update it.' >&2; exit 1 ;;
  esac
  branch=$(git -C "$install_dir" symbolic-ref --short HEAD) || {
    echo 'Updates require the main branch, not a detached checkout.' >&2
    exit 1
  }
  if [ "$branch" != main ]; then
    echo 'Updates require the main branch. Switch branches explicitly before retrying.' >&2
    exit 1
  fi
  changes=$(git -C "$install_dir" status --porcelain)
  if [ -n "$changes" ]; then
    echo 'The checkout has local changes; commit or move them before updating.' >&2
    exit 1
  fi
  echo "Updating $install_dir..."
  if ! git -C "$install_dir" pull --ff-only origin main; then
    echo 'Repository update failed; deployment was not started.' >&2
    exit 1
  fi
else
  if ! mkdir -p "$(dirname -- "$install_dir")"; then
    echo 'Cannot create the installation directory. Run as root or choose a writable XINCHEN_PANEL_DIR.' >&2
    exit 1
  fi
  echo "Downloading xinchen-panel to $install_dir..."
  if ! git clone --branch main --single-branch "$repo" "$install_dir"; then
    echo 'Repository clone failed; deployment was not started.' >&2
    exit 1
  fi
fi

if [ ! -f "$install_dir/scripts/deploy-vps.sh" ]; then
  echo 'The checkout is missing scripts/deploy-vps.sh.' >&2
  exit 1
fi
exec sh "$install_dir/scripts/deploy-vps.sh" "$@"
