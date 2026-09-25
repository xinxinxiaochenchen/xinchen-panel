# Single-Hop Lines Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Administrators can manage shared single-hop lines; members can create and manage their own single-hop lines within their current membership limits.

**Architecture:** A line remains separate from its proxy node and has one `line_hops` row with `position=0, role=egress`. `catalog` owns line validation, persistence, read authorization and audit records. Each write locks the relevant line or member row and checks the current membership snapshot and live node/group state in the same transaction. HTTP handlers apply session, CSRF and RBAC checks, then map stable domain errors.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16, standard `net/http`, OpenAPI 3.1.

---

## Task 1: Single-hop model

**Files:** `internal/catalog/line_model.go`, `internal/catalog/line_model_test.go`.

- [x] Test that `NormalizeLine` accepts a UUID proxy node, trimmed 1–100 character name, priority 0–1000, weight 1–100, at most 16 unique tags, and a boolean enabled flag. Test invalid node IDs, empty name, invalid priority/weight/tags, and attempted user multiplier override.
- [x] Implement `NewLine`, `LineInput`, `Line`, and `LineHop` with `node_id` as the sole hop input. A shared line may set `multiplier_milli` in 1–100000; a member line uses no multiplier override. The output exposes `hops:[{position:0,node_id,role:"egress"}]`.
- [x] Run `go test ./internal/catalog -run TestNormalizeLine -count=1` after the failing and passing states.

## Task 2: PostgreSQL create and read

**Files:** `internal/catalog/line_repository.go`, `internal/catalog/line_repository_test.go`.

- [x] Test shared creation, duplicate shared name conflict, member creation with an active membership whose snapshot grants the node group and permits custom lines, and rejection for missing membership, disabled group/node, non-proxy node, and max custom line count. Test a concurrent limit attempt through two transactions. Test that a shared line granted by membership becomes unavailable when its node/group is disabled or loses proxy capability.
- [x] Insert `lines`, exactly one `line_hops` row, and a redacted `audit_logs` row atomically. For member creation, lock the user row, then read one active membership (`status='active'`, `starts_at<=now()`, `ends_at>now()`) and its snapshot limits; reject when `allow_custom_lines=false` or current count reaches `max_custom_lines`. Lock node and resource group rows before validating enabled state, proxy capability, and `proxy_port`.
- [x] Add paginated `ListAllLines`, `ListAllowedLines`, `GetAllowedLine`. Shared visibility requires line grant **and** node group grant in the membership snapshot; custom visibility requires ownership and a current membership with the node group grant. Re-evaluate line, node and group state on every allowed read.
- [x] Run local catalog tests, then compile `go test -c` for Linux and run it against a freshly migrated disposable PostgreSQL 16 database on `us dmit` through Termark. Clean the test database and binary afterward.

## Task 3: Update and delete

**Files:** `internal/catalog/line_repository.go`, `internal/catalog/line_repository_test.go`, `internal/catalog/line_model.go`.

- [x] Test shared and owned update authorization, line disable, changed priority/weight/tags, immutable hop for this slice, and name conflicts. Test delete blocked by plan grants, proxy accesses or forwarding references; a successful owner delete must remove its hop and leave an audit row.
- [x] Implement `UpdateSharedLine`, `UpdateOwnLine`, and `DeleteOwnLine` transactionally with ownership checks. Keep the single-hop path immutable until a separate `PUT /admin/lines/{id}/hops` operation can validate all dependent resources.
- [x] Verify tests against PostgreSQL 16.

## Task 4: REST and docs

**Files:** `internal/platform/httpapi/lines.go`, `internal/platform/httpapi/lines_test.go`, `internal/platform/httpapi/server.go`, `cmd/control-plane/main.go`, `api/openapi/control-plane.yaml`, `README.md`.

- [x] Write handler tests for anonymous 401, member on admin route 403, write without CSRF 403, shared/admin create 201, own create 201, invalid body 400/422, absent entitlement rejection, owner isolation 404, and page cursors.
- [x] Register `GET/POST /api/v1/admin/lines`, `GET/POST /api/v1/lines`, `GET/PATCH /api/v1/lines/{id}`, and `DELETE /api/v1/lines/{id}`. Admin update can use `/api/v1/admin/lines/{id}`. Restrict member mutations to owned lines and current entitlement.
- [ ] Update OpenAPI and README. Run `go test ./... -count=1`, `go vet ./...`, YAML parse, `git diff --check`, review, and deploy while `CONTROL_BROWSER_AUTH_ENABLED=false` on the public HTTP preview. Confirm health 200 and line routes 404 over plain HTTP.
