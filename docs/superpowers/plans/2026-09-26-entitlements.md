# Plans and Membership Entitlements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Administrators can create a plan with explicit resource grants and assign a time-bounded membership; members can inspect the immutable authorization snapshot used by resource reads.

**Architecture:** `internal/entitlement` validates plan and membership requests, persists changes in PostgreSQL transactions with audit rows, and serializes an explicit snapshot. Membership assignment locks the user row and rejects overlapping live memberships. The HTTP layer applies existing session, RBAC and CSRF checks. Existing catalog queries continue to read `snapshot_json.resource_group_ids`.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16, standard `net/http`.

---

## Scope and contracts

- `POST /api/v1/admin/plans`: name, quota bytes, default multiplier, limits, resource group IDs, optional shared line IDs. Returns a plan with grant IDs and limits. Active plans only in this create path.
- `GET /api/v1/admin/plans`: cursor-paginated admin directory.
- `POST /api/v1/admin/memberships`: user ID, plan ID, start/end instants, anchor day and IANA timezone. Creates an immediately active membership; future scheduling is a later endpoint. The transaction copies plan quota, multiplier, limits and permitted group/line IDs into `snapshot_json` and writes an audit row.
- `GET /api/v1/my/membership` and `GET /api/v1/my/entitlements`: return only the authenticated user's current membership and its frozen snapshot. If none is active, return 404.
- No public HTTP deployment of these routes while browser auth remains disabled on the pure IP preview.

## Task 1: Domain input and snapshot

**Files:** `internal/entitlement/model.go`, `internal/entitlement/model_test.go`.

- [x] Write failing tests for duplicate grant IDs, invalid UUIDs, negative limits/quota, invalid timezone, membership end before start and future start. Run `go test ./internal/entitlement -run 'TestNormalize' -count=1` and see each invalid case rejected only after implementation.
- [x] Implement `NormalizePlan(NewPlan)`, `NormalizeMembership(NewMembership, now)` and typed `Snapshot`. Use `max_hops=1` for the MVP; carry `resource_group_ids` at the JSON top level for the existing catalog query.
- [x] Re-run the focused tests and keep JSON serialization deterministic by sorting grant IDs.

## Task 2: Transactional PostgreSQL repository

**Files:** `internal/entitlement/repository.go`, `internal/entitlement/repository_test.go`.

- [x] Write a real PostgreSQL integration test that creates a plan with one granted group, creates a member, then changes the plan grant table directly and proves the member's snapshot stays unchanged. Exercise duplicate active membership conflict and user-scoped read.
- [x] Implement plan creation and membership assignment. Validate referenced grant IDs in the transaction; insert `plans`, `plan_limits`, grants and `audit_logs` atomically. Lock the target user row before checking for an existing live membership. Map foreign key and unique violations to stable not-found/conflict errors.
- [x] Implement paginated plan listing and current membership reads. Run the focused PostgreSQL integration test in the isolated server test database.

## Task 3: HTTP authorization and API contract

**Files:** `internal/platform/httpapi/entitlement.go`, `internal/platform/httpapi/entitlement_test.go`, `internal/platform/httpapi/server.go`, `cmd/control-plane/main.go`, `api/openapi/control-plane.yaml`, `README.md`.

- [x] Write handler tests: missing session 401; member admin write 403; admin missing CSRF 403; valid admin create 201; member read uses authenticated own ID; unknown membership 404. Verify red tests.
- [x] Add the five endpoints, use `plans.write` for writes, `plans.read` for admin list, and `dashboard.read` for own reads. Reuse uniform JSON errors and list pagination, while keeping creation endpoints unavailable when browser auth is disabled.
- [x] Update OpenAPI schemas, parameter refs, and response codes. Parse YAML, run `go test ./... -count=1`, `go vet ./...`, `git diff --check`, and seek read-only code review.

Verification on 2026-09-26: Go tests and vet passed; OpenAPI YAML parsed; real PostgreSQL 16 integration passed with plan grants, frozen snapshots and catalog node authorization. Review found private-line grants and time zone/expiry edges; these were fixed and retested. The public IP preview was updated to `33201ce` with browser authentication still disabled.

## Deployment boundary

Run the PostgreSQL integration suite against an isolated database first. After review, publish binaries to `us dmit` with `CONTROL_BROWSER_AUTH_ENABLED=false` and verify health 200, login 404 and no exposed entitlement routes. The next stage adds user provisioning, plan edits, billing periods and shared/user single-hop lines; those operations must use the frozen snapshot authorization service.
