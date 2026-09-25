# User Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An administrator can create and list normal users so memberships can be assigned, and an authenticated user can rotate a password with all prior sessions revoked.

**Architecture:** `internal/identity` owns email/password validation and PostgreSQL transactions. User creation inserts the `user` role and a redacted audit row atomically. Password rotation locks the target user, verifies the old bcrypt hash, updates the hash, removes all browser sessions and records a secret-free audit row in one transaction. The HTTP layer uses existing session, RBAC, CSRF and error envelopes.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16, standard `net/http`.

---

## Task 1: Identity operations

**Files:** `internal/identity/accounts.go`, `internal/identity/accounts_test.go`.

- [x] Write failing tests for invalid email, password shorter than 12 bytes or longer than bcrypt's 72-byte limit, duplicate email, password rotation with wrong old password, and session revocation.
- [x] Implement `NormalizeNewUser`, transactional `CreateMember`, `ListUsers`, and `ChangePassword`. Input and audit output must never include or log a password hash or plaintext password. Creation assigns only the `user` role.
- [x] Run real PostgreSQL integration in a disposable server database containing migrations 1–3. Verify that a newly created user has member permissions, can authenticate, and old sessions fail after password change.

## Task 2: HTTP and API contract

**Files:** `internal/platform/httpapi/accounts.go`, `internal/platform/httpapi/accounts_test.go`, `internal/platform/httpapi/server.go`, `cmd/control-plane/main.go`, `api/openapi/control-plane.yaml`, `README.md`.

- [x] Write failing handler tests: anonymous admin create 401, member create 403, admin create without CSRF 403, valid admin create 201 without password in response, member list 403, own password change requires CSRF and returns 204.
- [x] Add `GET/POST /api/v1/admin/users` and `POST /api/v1/me/password`, using `users.read`, `users.write`, and an authenticated own session. Validate strict JSON input and size limits.
- [x] Update OpenAPI schemas and responses; run all Go tests, vet, YAML parse and read-only code review. Review found and tests covered login/password rotation concurrency and change password rate limiting.

## Deployment boundary

Publish to `us dmit` with browser auth disabled on the public HTTP IP. Confirm health 200 and user routes 404. User creation and password rotation become usable only after the user's HTTPS reverse proxy is ready and the listener is restricted to loopback.
