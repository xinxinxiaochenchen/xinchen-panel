# Node Forward Snapshot Implementation Plan

> **For agentic workers:** Use the approved network control plane architecture and test each behavior before implementing it. Steps use checkboxes for tracking.

**Goal:** Compile one node's current authorized direct forwarding rules into a deterministic, complete Agent runtime snapshot without exposing rules whose owner, membership, node, group, or target policy is no longer valid.

**Architecture:** `internal/orchestration` owns a pure compiler over explicit node and rule facts. A PostgreSQL reader collects those facts for one node in a read-only repeatable-read transaction. The compiler emits `agentruntime.Snapshot` with deterministic rule order and per-rule rejection diagnostics. Invalid rules are omitted so they cannot block unrelated revocations; a disabled node compiles an empty snapshot without reading its rules. This slice does not start an Agent stream or publish a revision; those follow after enrollment and mTLS are implemented.

**Tech Stack:** Go, pgx/v5, PostgreSQL 16, existing `agentruntime` validation.

## Task 1: Pure compiler

**Files:** `internal/orchestration/forward_snapshot.go`, `internal/orchestration/forward_snapshot_test.go`.

- [x] Write tests where one valid public target compiles, stale owners/memberships/groups/policies are omitted, BOTH needs both policy grants, and invalid/duplicate listeners are individually rejected. Run focused tests and observe failure.
- [x] Implement `CompileForwardSnapshot(node NodeFacts, rules []ForwardFacts, revision uint64) (CompiledForwardSnapshot, error)` with deterministic ordering, per-rule diagnostics and final `agentruntime.ValidateSnapshot` call. Run focused tests to green.

## Task 2: PostgreSQL fact reader

**Files:** `internal/orchestration/forward_repository.go`, `internal/orchestration/forward_repository_test.go`.

- [x] Write PostgreSQL integration tests with an isolated migrated database: create authorized and expired/revoked rules, then verify only current authorized rules appear in compiled snapshot. Confirm the test fails before implementation.
- [x] Implement a read-only repeatable-read query for node state, user and membership state, target node state, and active matching target policies. Return no active rules for disabled nodes/groups; fail on unknown node. Run integration tests to green.

## Task 3: Verification

**Files:** `README.md`, `progress.md`, `findings.md`.

- [x] Document snapshot compilation and its remaining transport gap.
- [x] Run `go test ./... -count=1`, `go vet ./...`, `git diff --check`, inspect staged changes, and commit.
