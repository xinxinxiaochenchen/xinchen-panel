# PostgreSQL Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Execute inline under the already confirmed architecture.

**Goal:** Give the control plane a real PostgreSQL connection, dependency-aware readiness, and an explicit migration command.

**Architecture:** `internal/platform/db` owns connection creation and schema migration. The HTTP layer accepts a small readiness interface and does not import database code. The service starts only when its configured database is reachable. Migration remains a separate command so schema changes are deliberate deployment steps.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16 SQL, standard `database/sql` interfaces.

---

## File map

| File | Purpose |
|---|---|
| `internal/platform/config/config.go` | Add required database URL for production run and optional test constructor |
| `internal/platform/db/connect.go` | Open pgx pool and ping database |
| `internal/platform/db/migrate.go` | Apply ordered migration files with checksum and advisory lock |
| `internal/platform/db/migrate_test.go` | Verify ordering, checksum mismatch and dirty schema failures through a narrow DB interface |
| `cmd/migrate/main.go` | Explicit `up` migration command |
| `cmd/control-plane/main.go` | Connect DB, wire readiness and close pool |
| `internal/platform/httpapi/server.go` | `GET /ready` pings dependency with timeout; liveness stays process-only |
| `internal/platform/httpapi/server_test.go` | Readiness success/failure behavior |
| `README.md` | Database setup and migration commands |

## Task 1: Readiness contract

- [x] Write an HTTP test with a checker function returning an error; `GET /api/v1/health/ready` must return 503 with code `DEPENDENCY_UNAVAILABLE`, while `/live` stays 200. Run package test and see the expected failure.
- [x] Change `NewHandler` to accept `ReadyChecker` (`Check(context.Context) error`), call it with a 2-second timeout from readiness, and keep the same error envelope. Implement a test checker in tests. Run HTTP package tests; expect pass.
- [x] Commit `feat: make readiness dependency aware`.

## Task 2: Database connection

- [x] Add `CONTROL_DATABASE_URL` to validated config; reject empty URL when starting the service, but allow a config test helper to pass a sample URL. Test invalid URL schemes and missing URL first; run test and observe failure.
- [x] Add pgx/v5 as dependency. Implement `db.Open(ctx,url) (*pgxpool.Pool,error)` with a 5-second connection timeout and ping. Wire it into `cmd/control-plane/main.go` and readiness checker. Run `go test ./...` and `go vet ./...`; expect pass.
- [x] Commit `feat: connect control plane to PostgreSQL`.

## Task 3: Migration command

- [x] Test that migration files are discovered in numeric order, have a stable SHA-256 checksum, and reject modified already-applied migrations. This test uses real SQL fixture files and the migration planner, not source text assertions. Run it red.
- [x] Implement `internal/platform/db/migrate.go`: create `schema_migrations(version bigint primary key, checksum text, applied_at timestamptz)`; acquire a PostgreSQL advisory lock; for each up SQL file, run its statements and metadata insert in one transaction; refuse a checksum mismatch. Remove explicit BEGIN/COMMIT from migration files so runner owns the transaction. Implement `cmd/migrate/main.go up` with database URL config. Run tests and vet.
- [ ] Execute the migration on a temporary PostgreSQL-compatible engine where possible, then commit `feat: add ordered transactional migrations`.

## Verification boundary

If no PostgreSQL network server is available locally, verify migration parsing and execution with PGlite and keep actual Go-to-PostgreSQL integration testing as an explicit remaining gate. Do not label readiness production ready until it has been tested against PostgreSQL 16.

## Verification record

- `go test ./...` and `go vet ./...` pass with pgx/v5 pinned.
- The up SQL creates 12 tables in PGlite and the down SQL removes all 12.
- The Go migration command has not been connected to a PostgreSQL 16 server in this workspace; this remains a deployment gate.
