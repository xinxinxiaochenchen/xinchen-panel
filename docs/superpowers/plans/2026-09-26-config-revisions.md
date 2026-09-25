# Agent Configuration Revision Store Implementation Plan

> **For agentic workers:** Implement each step with a failing test before production code. This follows the approved network control plane architecture.

**Goal:** Persist deterministic per-node forwarding snapshots as monotonic desired revisions and record idempotent Agent apply results.

**Architecture:** A PostgreSQL transaction locks the node's Agent row, compares the canonical snapshot digest with the latest desired revision, inserts only changed snapshots, and increments `agents.desired_revision`. An ACK/NACK references node, revision and digest; the store validates all three and advances `applied_revision` only for an applied result. This slice does not expose an Agent transport or activate forwarding on the server.

**Tech Stack:** Go, pgx/v5, PostgreSQL 16, SHA-256, JSON, migration version 5.

## Task 1: Deterministic snapshot digest

**Files:** `internal/orchestration/revision.go`, `internal/orchestration/revision_test.go`.

- [x] Write tests: identical rules in different input order produce one digest; payload changes alter digest; rejected-rule diagnostics do not affect executable digest. Observe red.
- [x] Implement canonical executable payload encoding and SHA-256 digest; run focused tests to green.

## Task 2: Persistence and result handling

**Files:** `migrations/000005_config_revisions.up.sql`, `.down.sql`, `internal/orchestration/revision_repository.go`, `internal/orchestration/revision_repository_test.go`.

- [x] Write PostgreSQL integration tests: first reconcile creates revision 1, replay keeps revision 1, changed payload creates revision 2, stale/unknown digest ACK is rejected, applied ACK advances `agents.applied_revision`, NACK keeps old applied revision, replay is idempotent. Observe red.
- [x] Add config revision migration and transactional reconcile/read/result methods; run the focused integration test against a disposable PostgreSQL 16 database to green.

## Task 3: Verification and documentation

**Files:** `README.md`, `progress.md`, `findings.md`.

- [x] Document reconcile/ACK state and remaining Agent enrollment/transport gaps.
- [x] Run all Go tests, vet, migration validation and diff check; review, commit and leave the public preview unchanged until transport exists.
