# Private Preview Deployment Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. The confirmed target is the saved Termark asset `us dmit`.

**Goal:** Deploy the current foundation to a private loopback address on `us dmit`, verify the PostgreSQL 16 migration and HTTP health, and avoid exposing an unfinished product publicly.

**Architecture:** Docker Compose runs a persistent PostgreSQL 16 container, a one-shot migration container, and the Go control plane. Linux amd64 binaries are cross-built locally, so the 1 GiB server does not compile Go. Compose maps only `127.0.0.1:18080` on the host. The existing Nginx Proxy Manager owns public 80/443 and is left untouched until the console domain and MVP are ready.

**Tech Stack:** Docker Compose v5, Go 1.27.1 multi-stage build, PostgreSQL 16, Debian 12 host.

---

## Files

- `deployments/compose/Dockerfile.prebuilt`: minimal runtime image for the memory-limited server.
- `deployments/compose/Dockerfile`: reproducible multi-stage build for larger hosts and CI.
- `scripts/build-linux-amd64.sh`: cross-build static binaries locally for the private preview.
- `deployments/compose/compose.yaml`: db, migration job, API service, loopback binding, limits and health checks.
- `deployments/compose/.env.example`: deployment environment names without secrets.
- `.dockerignore`: exclude Git and local files from build context.
- `.gitignore`: keep deployment `.env` and local output out of Git.
- `docs/deployment/private-preview.md`: exact deployment, verification, rollback and resource checks.

## Steps

1. Write the image and Compose files. Keep PostgreSQL private and map the API only to loopback. Use `depends_on: condition: service_completed_successfully` so migration finishes before API starts.
2. Validate local Go tests, Dockerfile references, SQL syntax and Compose YAML. On the server, run `docker compose config --quiet` before any `up`.
3. Inspect `/opt/network-control-plane` and occupied port 18080 on `us dmit`. Cross-build Linux amd64 binaries, prepare a release tarball without `.git` or `.env`, upload through Termark, and extract into a new release directory.
4. Generate a high-entropy database password on the server, store it in a mode-600 `.env`, then run `docker compose up -d --build`. Do not print the password to tool output.
5. Inspect container health, `schema_migrations`, table count, and call `http://127.0.0.1:18080/api/v1/health/live` and `/ready` from the server. Confirm host ports 80/443 and existing containers remain untouched.
6. Document the exact deployed commit and limitations. Do not configure public HTTPS or call this a complete control console until the MVP business modules and domain are ready.

## Rollback

`docker compose down` stops only this project and keeps its PostgreSQL volume. To return to the previous release, restore the previous release directory and run `docker compose up -d --build` from that directory. Never remove the volume as part of routine rollback.
