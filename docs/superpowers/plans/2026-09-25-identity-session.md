# Identity and Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persistent RBAC identities and secure browser sessions so later resource APIs can authorize each request.

**Architecture:** PostgreSQL stores users, role assignments and SHA-256 hashes of opaque session and CSRF tokens. `internal/identity` owns password verification and session lifecycle; `internal/platform/httpapi` owns cookie and JSON handling. The existing health-only public preview remains on the old release until HTTPS is available for login.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16, bcrypt, `net/http`.

---

## File map

| File | Responsibility |
|---|---|
| `migrations/000002_identity.up.sql` | RBAC and session tables with indexes and seed role/permission codes |
| `migrations/000002_identity.down.sql` | Reverse the migration for reviewed manual rollback |
| `internal/identity/service.go` | Password and token validation, session lifecycle |
| `internal/identity/repository.go` | pgx persistence for users and sessions |
| `internal/identity/service_test.go` | Service behavior with a narrow in-memory repository |
| `internal/platform/httpapi/auth.go` | Login, logout, current account, permission handlers |
| `internal/platform/httpapi/auth_test.go` | Cookie, CSRF and error contract tests |
| `cmd/control-plane/main.go` | Wire identity repository and handlers |
| `cmd/admin-bootstrap/main.go` | Create the first administrator from a password on stdin |
| `internal/platform/id/uuidv7.go` | Generate sortable UUIDv7 identifiers |
| `api/openapi/control-plane.yaml` | Document new endpoints and response schemas |

## Task 1: Persistent RBAC and sessions

- [x] Add a migration-loading test that requires version 2, run it and observe failure.
- [x] Add SQL tables `roles(code)`, `permissions(code)`, `user_roles(user_id, role_code)`, `role_permissions(role_code, permission_code)`, and `browser_sessions(token_hash, csrf_hash, user_id, expires_at)`; seed `admin` and `user` roles and explicit permissions. Run the migration-loading test.
- [x] Execute up and down SQL in an isolated PostgreSQL 16 database before any production migration.

## Task 2: Identity service

- [x] Test login succeeds only for an active user with a matching bcrypt hash; persisted values are SHA-256 hashes of the random tokens.
- [x] Test unknown email, wrong password and disabled account all return the same invalid-credentials result; no session is created.
- [x] Test authenticate rejects expired and revoked sessions; logout requires the matching CSRF token and revokes the session. PostgreSQL integration also checks absent sessions.
- [x] Implement repository interface and service with 12-hour session expiry, 32-byte random tokens, bcrypt verification, and constant-time CSRF comparison. Run package tests.

## Task 3: HTTP and production wiring

- [x] Test login response sets `HttpOnly`, `Secure`, `SameSite=Lax` cookie and returns CSRF token; `/me` requires the cookie; logout requires `X-CSRF-Token` and clears cookie.
- [x] Implement handlers and wire them into the existing request-ID/error middleware. Ensure errors never include password hashes or raw session tokens. Run HTTP package tests.
- [x] Implement pgx repository and wire it in `cmd/control-plane`; update OpenAPI. Run `go test ./...`, `go vet ./...`, `git diff --check`.

## Deployment boundary

The current public `http://IP:18080` preview must not serve login routes. Deploy the identity build only behind verified HTTPS, or keep the new build unpublished while the user prepares Nginx TLS. Do not weaken cookie security for the HTTP preview.

## Review fixes

- [x] Keep browser identity routes disabled by default through `CONTROL_BROWSER_AUTH_ENABLED`.
- [x] Limit login attempts before bcrypt work and return HTTP 429 with `Retry-After`.
- [x] Grant users `lines.write.self` while reserving `lines.write` for administrators.
- [x] Remove expired sessions in bounded hourly batches and verify active sessions survive cleanup.

## Task 4: Initial administrator

- [x] Test UUIDv7 encoding sets version 7 and RFC 4122 variant, and produces distinct IDs; then implement `internal/platform/id`.
- [x] Add a database integration test for administrator creation and duplicate-email rejection. Implement a transaction that inserts the user and `admin` role; bcrypt hash the supplied password and never print it.
- [x] Add `cmd/admin-bootstrap` reading `CONTROL_ADMIN_EMAIL` and password from non-interactive stdin, with a minimum 12-character password. Run Go tests and the PostgreSQL integration test in the isolated database.
