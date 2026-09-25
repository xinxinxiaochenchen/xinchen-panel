# Control Plane Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Execute inline in this task; the user already authorized coding after architecture confirmation.

**Goal:** Deliver a small, runnable Go control-plane service with validated configuration, uniform JSON errors, request logging, health endpoints, a starter PostgreSQL migration, and an OpenAPI contract.

**Architecture:** The Go binary owns startup and dependency wiring. Platform packages own configuration and HTTP concerns. SQL migration defines the first persistent catalog and identity tables, without exposing unfinished write APIs. This is the first independently verifiable part of the confirmed MVP; subsequent plans add authentication, catalog operations, Agent synchronization, billing and UI.

**Tech Stack:** Go 1.27 standard library, PostgreSQL 16 SQL, OpenAPI 3.1. No runtime Go dependencies in this phase.

---

## File map

| File | Responsibility |
|---|---|
| `go.mod` | Module and Go version |
| `cmd/control-plane/main.go` | Construct config, logger, server and graceful shutdown |
| `internal/platform/config/config.go` | Parse and validate environment configuration |
| `internal/platform/config/config_test.go` | Verify defaults and rejection of invalid values |
| `internal/platform/httpapi/errors.go` | Stable error envelope and JSON writing |
| `internal/platform/httpapi/server.go` | Routes, request ID, recovery and request logging |
| `internal/platform/httpapi/server_test.go` | Verify health, errors, request IDs and recovery |
| `migrations/000001_init.up.sql` | Initial identity/catalog tables and constraints |
| `migrations/000001_init.down.sql` | Reverse initial migration |
| `api/openapi/control-plane.yaml` | Public API contract for implemented endpoints |
| `README.md` | Local build and run instructions, explicit next milestones |

## Task 1: Toolchain and config

- [ ] Download the official macOS arm64 Go 1.27.1 archive into `/private/tmp`, verify its published SHA-256, and extract into `/private/tmp/network-control-plane-go`. Do not modify the system installation. Run `/private/tmp/network-control-plane-go/go/bin/go version`; expected `go version go1.27.1 darwin/arm64`.
- [ ] Create `go.mod` with `module controlplane` and `go 1.27.1`.
- [ ] Write `internal/platform/config/config_test.go` first. Test `LoadFrom` with an empty lookup (defaults to `127.0.0.1:8080`, `info`) and an invalid `CONTROL_HTTP_ADDR` or `CONTROL_LOG_LEVEL` (returns an error). Run `go test ./internal/platform/config`; expected failure because `LoadFrom` does not exist.
- [ ] Implement `Config{HTTPAddr string; LogLevel slog.Level}` and `LoadFrom(lookup func(string)(string,bool)) (Config,error)`. Require `net.SplitHostPort`; allow `debug`, `info`, `warn`, `error`. Run package tests; expected pass.
- [ ] Commit: `feat: add validated control plane configuration`.

## Task 2: HTTP contract

- [ ] Write `internal/platform/httpapi/server_test.go` first. Using `httptest`, assert `GET /health/live` returns 200 JSON `{"status":"ok"}`; unknown paths return 404 JSON envelope with `error.code=NOT_FOUND` and nonempty `request_id`; each response has `X-Request-ID`; handler panic produces 500 `INTERNAL` without leaking panic text. Run `go test ./internal/platform/httpapi`; expected compile failure because `NewHandler` does not exist.
- [ ] Implement `errors.go` with `ErrorResponse` and `WriteError(w,r,status,code,message)`; implementation always sets JSON content type and includes request ID from context.
- [ ] Implement `server.go` with `NewHandler(logger *slog.Logger) http.Handler`, route `GET /health/live`, `GET /health/ready` (returns 200 until a real dependency probe is wired), a JSON 404 fallback, request ID middleware, panic recovery, and structured request logging. Request IDs use `crypto/rand` and hex encoding; do not trust a caller supplied ID. Run `go test ./internal/platform/httpapi`; expected pass.
- [ ] Create `cmd/control-plane/main.go`: load config, build JSON `slog` logger, listen with `http.Server` timeouts, handle SIGINT/SIGTERM, and call `Shutdown` with 10-second context. Run `go test ./...` and `go vet ./...`; expected pass.
- [ ] Commit: `feat: add control plane HTTP foundation`.

## Task 3: Initial persistence contract

- [ ] Create `migrations/000001_init.up.sql` with `users`, `resource_groups`, `nodes`, `lines`, `line_hops`, `plans`, `plan_limits`, `plan_resource_group_grants`, `plan_line_grants`, `memberships`, and `outbox_events`. Include UUID primary keys, FK constraints, unique email/group code, node capability checks, line hop uniqueness, nonnegative limits, and indexes for common list queries. This migration is schema only; no destructive seed data.
- [ ] Create `migrations/000001_init.down.sql` dropping only these tables in reverse dependency order. Add a static test script or Go test reading the files and asserting all expected tables occur in both directions and no `CASCADE` is used; run it and confirm pass. A live PostgreSQL migration test belongs to the next phase because Docker/Postgres is not present locally.
- [ ] Create `api/openapi/control-plane.yaml` with `GET /api/v1/health/live`, `GET /api/v1/health/ready`, error schema and `X-Request-ID` response header. Align actual routes with this prefix. Add `README.md` build/run/test commands and precise limitations of this phase.
- [ ] Run `go test ./...`, `go vet ./...`, `git diff --check`, and smoke test the binary using a local port and `curl`. Expected: tests and vet pass; liveness and readiness return 200 JSON; an unknown route returns the documented error envelope.
- [ ] Commit: `feat: add initial schema and API contract`.

## Scope and next plans

The next vertical slices are: identity and RBAC; node/line/catalog APIs; Agent enrollment and config convergence; proxy access and forwarding; subscriptions/routing; quota and usage; SaaS console UI; deployment and end-to-end verification. Each slice needs its own focused plan and working tests. The foundation does not claim those features are implemented.
