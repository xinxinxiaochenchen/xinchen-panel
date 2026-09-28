#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: sh scripts/deploy-vps.sh [--public-preview]

Build and start the WebUI control plane with Docker Compose. By default the
HTTP port is available only on 127.0.0.1:18080 for an HTTPS reverse proxy.
--public-preview exposes a read-only HTTP preview on port 18080.
EOF
}

public_preview=false
case "${1:-}" in
  '') ;;
  --public-preview) public_preview=true ;;
  --help|-h) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

root=${NCP_DEPLOY_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}
compose_dir=$root/deployments/compose
env_file=$compose_dir/.env
backup_dir=$root/.local/backups
if [ ! -f "$compose_dir/compose.yaml" ] || [ ! -f "$compose_dir/compose.source.yaml" ]; then
  echo 'Missing Docker Compose files. Run this script from a complete source checkout.' >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo 'Docker Engine and the Compose plugin must be installed and usable by this account.' >&2
  exit 1
fi

volume_name=network-control-plane_postgres_data
existing_volume=false
if docker volume inspect "$volume_name" >/dev/null 2>&1; then existing_volume=true; fi
if [ ! -f "$env_file" ] && [ "$existing_volume" = true ]; then
  echo 'An existing database volume was found without this checkout’s .env. Restore the original .env before upgrading; refusing to generate a new password for the existing database.' >&2
  exit 1
fi

umask 077
if [ ! -f "$env_file" ]; then
  password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  if [ "${#password}" -ne 64 ]; then echo 'Failed to generate a database password.' >&2; exit 1; fi
  bind_ip=127.0.0.1
  if [ "$public_preview" = true ]; then bind_ip=0.0.0.0; fi
  temp_env=$(mktemp "$compose_dir/.env.XXXXXX")
  trap 'rm -f "$temp_env"' EXIT HUP INT TERM
  {
    printf 'POSTGRES_PASSWORD=%s\n' "$password"
    printf 'CONTROL_BIND_IP=%s\n' "$bind_ip"
    printf 'CONTROL_BROWSER_AUTH_ENABLED=false\n'
  } > "$temp_env"
  chmod 600 "$temp_env"
  mv "$temp_env" "$env_file"
  trap - EXIT HUP INT TERM
  echo "Created private Compose configuration: $env_file"
else
  auth_value=$(sed -n 's/^[[:space:]]*CONTROL_BROWSER_AUTH_ENABLED[[:space:]]*=[[:space:]]*//p' "$env_file" | tail -n 1 | sed 's/[[:space:]]*#.*$//;s/[[:space:]]*$//' | tr '[:upper:]' '[:lower:]')
  if [ -n "$auth_value" ] && [ "$auth_value" != false ] && [ "$auth_value" != 0 ]; then
    echo 'This script handles read-only preview installs. Use the HTTPS deployment guide for browser authentication.' >&2
    exit 1
  fi
  bind_value=$(sed -n 's/^[[:space:]]*CONTROL_BIND_IP[[:space:]]*=[[:space:]]*//p' "$env_file" | tail -n 1 | sed 's/[[:space:]]*#.*$//;s/[[:space:]]*$//')
  if [ -z "$bind_value" ]; then bind_value=127.0.0.1; fi
  expected_bind=127.0.0.1
  if [ "$public_preview" = true ]; then expected_bind=0.0.0.0; fi
  if [ "$bind_value" != "$expected_bind" ]; then
    echo "Existing .env binds to $bind_value; expected $expected_bind for the selected mode. Edit CONTROL_BIND_IP explicitly." >&2
    exit 1
  fi
  chmod 600 "$env_file"
fi

compose() {
  docker compose -f "$compose_dir/compose.yaml" -f "$compose_dir/compose.source.yaml" --env-file "$env_file" "$@"
}

if [ "$existing_volume" = true ]; then
  echo 'Starting the existing PostgreSQL service for a pre-migration backup...'
  compose up -d --wait db
  mkdir -p "$backup_dir"
  backup=$backup_dir/controlplane-$(date -u +%Y%m%dT%H%M%SZ).dump
  if ! compose exec -T db pg_dump -U controlplane -d controlplane --format=custom > "$backup"; then
    rm -f "$backup"
    echo 'Database backup failed; deployment stopped before migration.' >&2
    exit 1
  fi
  if [ ! -s "$backup" ] || ! compose exec -T db pg_restore --list < "$backup" >/dev/null; then
    rm -f "$backup"
    echo 'Database backup could not be verified; deployment stopped before migration.' >&2
    exit 1
  fi
  echo "Verified pre-migration backup: $backup"
fi

echo 'Building and starting PostgreSQL, migration, and WebUI...'
compose up -d --build db migrate api

attempt=0
while [ "$attempt" -lt 30 ]; do
  if compose exec -T api /usr/local/bin/control-plane --healthcheck >/dev/null 2>&1; then
    echo 'WebUI control plane is ready on port 18080.'
    if [ "$public_preview" = true ]; then
      echo 'Read-only HTTP preview is bound to 0.0.0.0:18080.'
    else
      echo 'HTTP is bound to 127.0.0.1:18080; configure an HTTPS reverse proxy to access the WebUI.'
    fi
    exit 0
  fi
  attempt=$((attempt + 1))
  sleep 2
done
echo 'The API did not become ready; inspect logs with docker compose in deployments/compose.' >&2
exit 1
