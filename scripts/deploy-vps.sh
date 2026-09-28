#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: sh scripts/deploy-vps.sh [--public-http | --https | --public-preview]

Build and run the WebUI with Docker Compose.
First install defaults to a functional HTTP panel on 0.0.0.0:18080.
Updates without a flag preserve the existing access mode and configuration.
--public-http: enable login and management over IP HTTP.
--https: enable login with Secure cookies on 127.0.0.1:18080 for your HTTPS proxy.
--public-preview: expose only the read-only HTTP preview.

First formal install generates a private one-time setup credential.
Create the administrator and configure business parameters in the browser.
No administrator email, password or business configuration is read by this script.
EOF
}

requested_mode=
case "${1:-}" in
  '') ;;
  --public-http) requested_mode=http ;;
  --https) requested_mode=https ;;
  --public-preview) requested_mode=preview ;;
  --help|-h) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

root=${NCP_DEPLOY_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}
compose_dir=$root/deployments/compose
env_file=$compose_dir/.env
backup_dir=$root/.local/backups
for required in compose.yaml compose.source.yaml compose.proxy-secrets.yaml; do
  if [ ! -f "$compose_dir/$required" ]; then
    echo 'Missing Docker Compose files. Run from a complete source checkout.' >&2
    exit 1
  fi
done
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
if [ -L "$env_file" ]; then echo 'Compose .env must not be a symbolic link.' >&2; exit 1; fi

umask 077
read_env() {
  sed -n "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*//p" "$env_file" | tail -n 1 | sed "s/[[:space:]]*#.*$//;s/[[:space:]]*$//;s/^\"\(.*\)\"$/\1/;s/^'\(.*\)'$/\1/"
}
read_bool_env() {
  task_bool_value=$(read_env "$1" | tr '[:upper:]' '[:lower:]')
  case "$task_bool_value" in
    true|t|1) printf 'true\n' ;;
    false|f|0) printf 'false\n' ;;
    '') printf '%s\n' "$2" ;;
    *) echo "Invalid $1 in .env; expected true or false." >&2; return 1 ;;
  esac
}
write_env() {
  task_env_tmp=$(mktemp "$compose_dir/.env.XXXXXX")
  awk -v key="$1" '$0 !~ "^[[:space:]]*" key "[[:space:]]*=" {print}' "$env_file" > "$task_env_tmp"
  printf '%s=%s\n' "$1" "$2" >> "$task_env_tmp"
  chmod 600 "$task_env_tmp"
  mv "$task_env_tmp" "$env_file"
}

old_auth=false
if [ ! -f "$env_file" ]; then
  password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  if [ "${#password}" -ne 64 ]; then echo 'Failed to generate a database password.' >&2; exit 1; fi
  printf 'POSTGRES_PASSWORD=%s\n' "$password" > "$env_file"
  mode=${requested_mode:-http}
else
  chmod 600 "$env_file"
  old_auth=$(read_bool_env CONTROL_BROWSER_AUTH_ENABLED false)
  mode=$(read_env CONTROL_DEPLOYMENT_MODE)
  if [ -z "$mode" ]; then
    if [ "$old_auth" = false ]; then mode=preview
    elif [ "$(read_bool_env CONTROL_BROWSER_COOKIE_SECURE true)" = false ]; then mode=http
    else mode=https; fi
  fi
  if [ -n "$requested_mode" ]; then mode=$requested_mode; fi
fi
case "$mode" in http|https|preview) ;; *) echo 'Invalid CONTROL_DEPLOYMENT_MODE in .env.' >&2; exit 1 ;; esac

# An explicit mode switch updates only access settings; all other settings stay.
# No-flag upgrades keep custom bind addresses and existing settings.
if [ -n "$requested_mode" ] || [ -z "$(read_env CONTROL_BIND_IP)" ]; then
  bind_ip=0.0.0.0
  if [ "$mode" = https ]; then bind_ip=127.0.0.1; fi
  write_env CONTROL_BIND_IP "$bind_ip"
fi
expected_auth=true
cookie_secure=true
if [ "$mode" = preview ]; then expected_auth=false; fi
if [ "$mode" = http ]; then cookie_secure=false; fi
if [ -n "$requested_mode" ] || [ -z "$(read_env CONTROL_BROWSER_AUTH_ENABLED)" ]; then
  write_env CONTROL_BROWSER_AUTH_ENABLED "$expected_auth"
fi
if [ -n "$requested_mode" ] || [ -z "$(read_env CONTROL_BROWSER_COOKIE_SECURE)" ]; then
  write_env CONTROL_BROWSER_COOKIE_SECURE "$cookie_secure"
fi
if [ "$(read_env CONTROL_DEPLOYMENT_MODE)" != "$mode" ]; then write_env CONTROL_DEPLOYMENT_MODE "$mode"; fi
if [ "$(read_bool_env CONTROL_BROWSER_AUTH_ENABLED false)" != "$expected_auth" ] || [ "$(read_bool_env CONTROL_BROWSER_COOKIE_SECURE true)" != "$cookie_secure" ]; then
  echo 'Access settings conflict with CONTROL_DEPLOYMENT_MODE; select --public-http, --https or --public-preview explicitly.' >&2
  exit 1
fi

secret_dir=
if [ "$mode" != preview ]; then
  secret_dir=$(read_env CONTROL_PROXY_CREDENTIAL_SECRET_DIR)
  if [ -z "$secret_dir" ]; then secret_dir=$root/.local/secrets/proxy; fi
  case "$secret_dir" in /*) ;; *) echo 'CONTROL_PROXY_CREDENTIAL_SECRET_DIR must be absolute.' >&2; exit 1 ;; esac
  if [ -L "$secret_dir" ] || [ -L "$secret_dir/proxy.key" ] || [ -L "$secret_dir/setup.token" ]; then
    echo 'Secret paths must not be symbolic links.' >&2; exit 1
  fi
  if [ ! -f "$secret_dir/proxy.key" ]; then
    # A temporary preview keeps the formal database and its key path. Losing
    # that key must not become permission to replace encrypted credentials.
    if [ "$existing_volume" = true ] && { [ "$old_auth" = true ] || [ -n "$(read_env CONTROL_PROXY_CREDENTIAL_SECRET_DIR)" ]; }; then
      echo 'The existing proxy credential key is missing. Restore the original key; refusing to rekey an authenticated database.' >&2
      exit 1
    fi
    mkdir -p "$secret_dir"
    chmod 711 "$secret_dir"
    key=$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')
    if [ "${#key}" -ne 43 ]; then echo 'Failed to generate the proxy credential key.' >&2; exit 1; fi
    printf '%s\n' "$key" > "$secret_dir/proxy.key"
    chmod 600 "$secret_dir/proxy.key"
    unset key
  fi
  if [ "$(read_env CONTROL_PROXY_CREDENTIAL_SECRET_DIR)" != "$secret_dir" ]; then
    write_env CONTROL_PROXY_CREDENTIAL_SECRET_DIR "$secret_dir"
  fi
  if [ -e "$secret_dir/setup.token" ] && [ ! -f "$secret_dir/setup.token" ]; then
    echo 'The setup credential must be a regular file.' >&2; exit 1
  fi
  if [ ! -f "$secret_dir/setup.token" ]; then
    setup_token=$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')
    if [ "${#setup_token}" -ne 43 ]; then echo 'Failed to generate the setup credential.' >&2; exit 1; fi
    printf '%s\n' "$setup_token" > "$secret_dir/setup.token"
    chmod 600 "$secret_dir/setup.token"
    unset setup_token
  fi
  if [ "$(read_env CONTROL_SETUP_TOKEN_FILE)" != /run/proxy-secrets/setup.token ]; then
    write_env CONTROL_SETUP_TOKEN_FILE /run/proxy-secrets/setup.token
  fi
fi

compose() {
  set -- -f "$compose_dir/compose.yaml" -f "$compose_dir/compose.source.yaml" "$@"
  if [ "$mode" != preview ]; then set -- -f "$compose_dir/compose.proxy-secrets.yaml" "$@"; fi
  # Preserve existing optional node-control settings when updating the API.
  if [ -n "$(read_env CONTROL_AGENT_TLS_DIR)" ]; then set -- -f "$compose_dir/compose.agent-tls.yaml" "$@"; fi
  if [ -n "$(read_env CONTROL_RELAY_SECRET_DIR)" ]; then set -- -f "$compose_dir/compose.relay-secrets.yaml" "$@"; fi
  docker compose --project-name network-control-plane --project-directory "$compose_dir" --env-file "$env_file" "$@"
}

if [ "$existing_volume" = true ]; then
  echo 'Starting PostgreSQL for a pre-migration backup...'
  compose up -d --wait db
  mkdir -p "$backup_dir"
  backup=$(mktemp "$backup_dir/controlplane-$(date -u +%Y%m%dT%H%M%SZ).dump.XXXXXX")
  if ! compose exec -T db pg_dump -U controlplane -d controlplane --format=custom > "$backup"; then
    rm -f "$backup"
    echo 'Database backup failed; deployment stopped before migration.' >&2; exit 1
  fi
  if [ ! -s "$backup" ] || ! compose exec -T db pg_restore --list < "$backup" >/dev/null; then
    rm -f "$backup"
    echo 'Database backup could not be verified; deployment stopped before migration.' >&2; exit 1
  fi
  echo "Verified pre-migration backup: $backup"
fi

echo 'Building the control plane and WebUI...'
compose build migrate api
compose up -d --wait db
if [ "$mode" != preview ]; then
  # The application runs as UID 65532. Reuse the DB image to fix ownership,
  # including retries after a failed build and non-root Docker group installs.
  docker run --rm --network none --user 0 --entrypoint chmod \
    -v "$secret_dir:/run/proxy-secrets" postgres:16.10-bookworm \
    600 /run/proxy-secrets/proxy.key /run/proxy-secrets/setup.token
  docker run --rm --network none --user 0 --entrypoint chown \
    -v "$secret_dir:/run/proxy-secrets" postgres:16.10-bookworm \
    65532:65532 /run/proxy-secrets/proxy.key /run/proxy-secrets/setup.token
fi
echo 'Applying database migrations...'
if ! compose run --rm -T --no-deps migrate; then
  echo 'Database migration failed; the API was not restarted.' >&2; exit 1
fi

admin_state=configured
if [ "$mode" != preview ]; then
  if ! admin_state=$(compose run --rm -T --no-deps api /usr/local/bin/admin-bootstrap --status); then
    echo 'Unable to read administrator state; the API was not restarted.' >&2; exit 1
  fi
  case "$admin_state" in
    configured) ;;
    empty) ;;
    *) echo 'Invalid administrator state; the API was not restarted.' >&2; exit 1 ;;
  esac
fi

compose up -d --no-deps api
attempt=0
while [ "$attempt" -lt 30 ]; do
  if compose exec -T api /usr/local/bin/control-plane --healthcheck >/dev/null 2>&1; then
    case "$mode" in
      http) echo 'Panel ready: http://SERVER_IP:18080/ (login and management enabled).' ;;
      https) echo 'Panel ready on 127.0.0.1:18080. Access it through your HTTPS reverse proxy.' ;;
      preview) echo 'Read-only HTTP preview ready on port 18080.' ;;
    esac
    if [ "$admin_state" = empty ]; then
      echo 'Open the panel in your browser to create the administrator and configure business parameters.'
      printf 'One-time setup credential: '
      docker run --rm --network none --user 0 --entrypoint cat \
        -v "$secret_dir:/run/proxy-secrets:ro" postgres:16.10-bookworm \
        /run/proxy-secrets/setup.token
      echo 'Keep this credential private. The setup entry closes after administrator creation.'
    fi
    exit 0
  fi
  attempt=$((attempt + 1))
  sleep 2
done
echo 'The API did not become ready; inspect logs in deployments/compose.' >&2
exit 1
