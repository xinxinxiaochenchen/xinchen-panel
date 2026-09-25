# Resource Directory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let administrators create resource groups and nodes, and let members list only nodes allowed by their active membership snapshot.

**Architecture:** `internal/catalog` owns validation and PostgreSQL access. Administrator writes are transactional with audit entries. User lists read frozen `memberships.snapshot_json.resource_group_ids`, so later plan edits cannot silently expand access. `internal/platform/httpapi` enforces session, RBAC, CSRF and JSON errors before calling catalog services.

**Tech Stack:** Go 1.27.1, pgx/v5, PostgreSQL 16, standard `net/http`.

---

## File map

| File | Responsibility |
|---|---|
| `migrations/000003_catalog_audit.up.sql` | Audit table and unique proxy endpoint index |
| `migrations/000003_catalog_audit.down.sql` | Reviewed manual rollback |
| `internal/catalog/model.go` | Group/node DTOs and input validation |
| `internal/catalog/repository.go` | Transactional writes and plan-scoped reads |
| `internal/catalog/*_test.go` | Domain and real PostgreSQL integration tests |
| `internal/platform/httpapi/catalog.go` | Admin and member resource routes |
| `internal/platform/httpapi/catalog_test.go` | RBAC, CSRF, error and response tests |
| `cmd/control-plane/main.go` | Wire catalog repository |
| `api/openapi/control-plane.yaml` | Publish directory API contract |

## Task 1: Audit migration

- [x] Change migration loader test to require versions 1 through 3 and watch it fail.
- [x] Add `audit_logs` with actor/action/object/request ID and redacted JSON payload, plus a unique `(lower(host), proxy_port)` index for non-null proxy ports.
- [x] Apply and roll back migration 3 in an isolated PostgreSQL 16 database.

## Task 2: Catalog validation and persistence

- [x] Write failing validation tests for group code, host, capabilities, port and multiplier; implement focused DTO validation.
- [x] Write a real PostgreSQL integration test: create two groups and nodes, confirm an active membership with one frozen group ID only sees that group's enabled node; expired membership sees none.
- [x] Implement transactional group/node creation with UUIDv7 IDs and audit rows, and list/read queries. Verify duplicate proxy endpoint is a conflict.

## Task 3: HTTP permissions and contract

- [x] Write handler tests: unauthenticated is 401; member POST is 403; administrator POST without CSRF is 403; valid administrator POST returns 201; member GET returns scoped items.
- [x] Add `POST/GET /api/v1/admin/resource-groups`, `POST/GET /api/v1/admin/nodes`, `GET /api/v1/nodes`, `GET /api/v1/nodes/{id}`. Require `nodes.write` for admin endpoints and `nodes.read` for member endpoints.
- [x] Wire the handler only when browser auth is enabled; update OpenAPI; run all Go tests, vet, YAML checks and real PostgreSQL integration.

Verification on 2026-09-26: Go test suite and vet passed; OpenAPI YAML parsed; isolated PostgreSQL 16 catalog integration test passed; migration 3 down removed both new objects. Read-only review found two P2 issues, both fixed and retested: pagination parameters in OpenAPI and `proxy_port` validation for forward-only nodes.

## Deployment boundary

Keep browser auth disabled on the public HTTP IP preview. Migration 3 can be deployed after backup, but resource writes remain unavailable until HTTPS and administrator authentication are enabled. The next plan covers plans, memberships and frozen entitlement creation through REST, followed by shared/user single-hop lines.
