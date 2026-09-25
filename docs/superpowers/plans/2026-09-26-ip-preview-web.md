# Pure IP Read-only Console Preview Implementation Plan

> **For agentic workers:** Implement the HTTP behavior with failing tests first. The visual shell is a reversible preview and is verified by a production build and browser inspection.

**Goal:** Serve an original, responsive React console shell at the existing pure IP address, showing real API readiness without exposing login or fabricated account data.

**Architecture:** Vite produces static assets under `apps/web/dist`. The Go server wraps the existing API handler only when `CONTROL_WEB_DIR` is configured, serving static assets and single-page routes while preserving `/api/*`. Compose copies the prebuilt bundle into the API image. Public HTTP remains read-only; browser identity routes stay disabled until secure access is configured.

**Tech Stack:** React, TypeScript, Vite, Tailwind CSS, Lucide icons, Go `fs.FS`, existing Docker Compose.

## Task 1: Static hosting boundary

**Files:** `internal/platform/httpapi/web.go`, `web_test.go`, `internal/platform/config/config.go`, `cmd/control-plane/main.go`.

- [x] Write tests: `/api/v1/health/live` reaches the API, `/` and an SPA route serve HTML, known assets use their MIME types, missing assets return 404, write methods cannot mutate static paths.
- [x] Implement the static wrapper and optional `CONTROL_WEB_DIR`; the API remains available without a web bundle.

## Task 2: Original UI shell

**Files:** `apps/web/*`.

- [x] Build a responsive seven-item navigation, theme toggle, clear pure IP preview badge, actual readiness indicator, and honest empty states for unconnected modules.
- [x] Run typecheck and production build; inspect desktop and mobile widths in a real browser.

## Task 3: Deployment packaging

**Files:** `deployments/compose/Dockerfile.prebuilt`, `deployments/compose/compose.yaml`, `scripts/build-linux-amd64.sh`, `README.md`, `docs/deployment/private-preview.md`.

- [x] Package web assets with the Go release and set `CONTROL_WEB_DIR` in Compose while retaining the existing 18080 port and private database.
- [x] Run Go tests, vet, frontend build, HTTP smoke test, and diff check. Back up the formal database, deploy to `us dmit` through Termark, apply migration 5, and verify public IP page and health without enabling login.
