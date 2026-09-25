# Direct Forward Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** A member can reserve and manage an authorized TCP, UDP, or BOTH ingress port that forwards directly to a public host or an authorized target node. Rule changes are durable, audited, and queued for later Agent convergence.

**Architecture:** `internal/forward` owns validation, transactional persistence, ownership checks, administrator target policies, port reservations and outbox events. HTTP handlers use the existing secure session, CSRF and RBAC middleware. A disabled rule keeps its port; deletion releases it. `apply_status=pending` means the rule has not yet been applied by an Agent; this slice does not claim data-plane execution. A `line_id` is reserved in the schema for later multi-hop support, but accepted requests must not specify one in this direct-only slice.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16, `net/http`, OpenAPI 3.1.

## Contract and invariants

- `NewRule` fields: `name`, `ingress_node_id`, `ingress_port`, exactly one of `target_node_id` or `target_host`, `target_port`, `protocol` (`TCP`, `UDP`, `BOTH`), optional `enabled`. A target node is dialed at its administrator-controlled `public_ip` or `host`; a host target must be a public IP literal or DNS name. The future Agent must resolve DNS and reject non-public resolved addresses at connection time.
- `RulePatch` can change `name` and `enabled`. Endpoint and protocol changes require deletion and recreation so reservations remain atomic and unambiguous. The API rejects `line_id` until a multi-hop execution path exists.
- Ingress requires an enabled `forward` node in an enabled resource group granted by a current active membership. Target-node mode requires the target node/group to be enabled and granted. Membership snapshot `limits.max_forward_rules_per_node` counts both enabled and disabled rules.
- Destination policy is default-deny. An administrator creates enabled `forward_target_policies` by destination kind (`public_host` or `node` with an explicit target resource group), physical protocol (`TCP` or `UDP`) and inclusive destination port range. BOTH rules require a matching policy for each physical protocol. Creation and re-enabling lock matching policy rows; a future Agent must recheck the current policy when compiling and applying configuration.
- Lock the owner row before counting rules. Insert one TCP and/or one UDP active `port_allocations` row in the same transaction; a partial unique index prevents a second owner from reserving an occupied node/protocol/port. For a BOTH rule, either both protocols reserve or neither does.
- Creation and re-enabling recheck membership expiry immediately before commit. Disabling/deleting an owned rule remains possible after membership expiry. Existing rules remain visible to their owner. Create, update and delete write redacted audit entries and an outbox event in the transaction; `apply_status` stays pending until Agent ACK is implemented.

## Task 1: Schema and model

**Files:** `migrations/000004_forward_rules.up.sql`, `.down.sql`, `internal/platform/db/migrate_test.go`, `internal/forward/model.go`, `model_test.go`, `internal/forward/policy_model.go`, `policy_model_test.go`.

- [x] Add a failing migration-loader test expecting version 4; run `go test ./internal/platform/db -run TestRepositoryMigrationLoads -count=1` and confirm the expected failure. Add the migration with `forward_rules` and `port_allocations`, ownership/target/protocol checks, unique owner name, and partial unique active port index. Re-run the test.
- [x] Write failing table tests for accepted TCP/UDP/BOTH requests; trim name and DNS host; reject missing/dual targets, invalid UUIDs, malformed hosts, private IP literals, invalid ports/protocol, and `line_id`. Run `go test ./internal/forward -run TestNormalizeRule -count=1` and confirm failure before implementing `NormalizeRule(NewRule) (RuleInput,error)`.
- [x] Add patch normalization tests for empty patch, invalid name, and valid enabled/name changes; implement `NormalizeRulePatch(RulePatch)`. Run package tests.
- [x] Add `forward_target_policies` and an administrator-only `forward_policies.write` RBAC grant in migration 4. Test policy normalization for kind, group, protocol and port range. A new installation contains no policy rows.

## Task 2: Transactional repository

**Files:** `internal/forward/repository.go`, `repository_test.go`.

- [x] Write a PostgreSQL 16 integration test for authorized creation, inactive membership, denied ingress/target groups, disabled/non-forward ingress, max-per-node count including disabled rules, public-host target, and node target. Run the Linux test binary against an independently migrated disposable database through Termark; confirm the test fails before implementation.
- [x] Implement `CreateRule`, `ListOwnRules`, and `GetOwnRule`. Lock the owner user row, active membership row and node/group rows. Recheck expiration before commit. Return stable `ErrNotFound`, `ErrConflict`, `ErrLimitReached` and validation errors. Write audit and outbox rows in the same transaction.
- [x] Before reserving a port, require matching enabled destination policies for every physical protocol and lock them `FOR SHARE`. Return `ErrPolicyDenied` if absent. Add tests for default deny, wrong kind/group/port, and revocation before re-enable.
- [x] Extend the integration test for parallel rule creation at the per-node limit and competing owners reserving the same TCP/UDP port. Confirm exactly one succeeds, and a failed BOTH allocation leaves no partial reservation.

## Task 3: Mutation and release

**Files:** `internal/forward/repository.go`, `repository_test.go`.

- [x] Write failing integration tests for owner isolation, rename conflict, disable and re-enable, retained reservation while disabled, expired membership preventing re-enable, deletion releasing both protocol reservations, and outbox/audit records.
- [x] Implement `UpdateOwnRule` and `DeleteOwnRule` with rule row locks. Name/enable changes leave endpoint immutable. Re-enable validates current entitlement and ingress/target state. Delete marks all reservations released before deleting the rule; database uniqueness protects concurrent creators.
- [x] Re-run the PostgreSQL 16 integration suite, including a repeated concurrency test. Clean the disposable database and test binary.

## Task 4: REST and release

**Files:** `internal/forward/policy_repository.go`, `policy_repository_test.go`, `internal/platform/httpapi/forward.go`, `forward_test.go`, `forward_policy.go`, `forward_policy_test.go`, `server.go`, `cmd/control-plane/main.go`, `api/openapi/control-plane.yaml`, `README.md`, `docs/deployment/private-preview.md`.

- [x] Write failing HTTP tests for 401, RBAC 403, CSRF 403, create 201, malformed JSON 400, validation 422, limit/port conflicts 409, owner-only list/detail/update/delete, cursor pagination, and auth-disabled route 404.
- [x] Register `GET/POST /api/v1/forward-rules` and `GET/PATCH/DELETE /api/v1/forward-rules/{id}`; wire the repository only when browser auth is enabled. Document schemas and stable error codes in OpenAPI.
- [x] Add administrator `GET/POST /api/v1/admin/forward-target-policies` and `PATCH/DELETE /api/v1/admin/forward-target-policies/{id}` with CSRF and `forward_policies.write`. Changes write audit/outbox records and mark affected rules pending for later Agent convergence.
- [x] Run `go test ./... -count=1`, `go vet ./...`, OpenAPI YAML parse and `git diff --check`. Review the diff and commit. Back up the production database, cross-build a Linux release, apply migration 4 on `us dmit`, and verify database/container health and public IP route behavior with browser auth still disabled.
