# Forward Configuration Convergence Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Execute inline in the existing isolated worktree.

**Goal:** Turn durable forwarding changes and time-based authorization changes into per-node desired configuration revisions.

**Architecture:** A small worker claims relevant outbox rows with PostgreSQL `FOR UPDATE SKIP LOCKED`, calls the existing transaction-safe `RevisionRepository.Reconcile`, and marks events processed only after reconciliation succeeds. Reconciliation uses its own transaction; a crash before event completion may replay it, and digest comparison makes replay idempotent. A policy change safely reconciles all enrolled Agent nodes because legacy policy events contain no old policy scope and deletion removes the policy row. Failed events retain the original row with bounded retry delay. A periodic sweep independently reconciles all Agent nodes so membership expiry, disabled accounts, and other time-based authorization changes converge without outbox writes.

**Tech Stack:** Go 1.27, pgx/v5, PostgreSQL 16, existing `internal/orchestration` compiler and revision store.

---

### Task 1: Event worker behavior

**Files:**
- Create: `internal/orchestration/convergence_worker_test.go`
- Create: `internal/orchestration/convergence_worker.go`

- [x] **Step 1:** Write a PostgreSQL integration test that inserts a `forward_rule.changed` event with `payload.node_id`, calls `ProcessOne`, and expects one desired revision plus `processed_at` populated. A second call must report no event. Add a `forward_policy.changed` event and two Agent nodes; both must be reconciled. Add an unrelated kind and verify it remains for its own consumer.
- [x] **Step 2:** Run the test before implementation; expect the missing worker API to fail compilation.
- [x] **Step 3:** Implement `NewConvergenceWorker(pool, revisions)`, `ProcessOne(ctx) (bool,error)` and the limited event query. Keep the event row locked until all affected nodes are reconciled, then mark it processed in the same event transaction. Use a per-event transaction so a failed event does not prevent another event from running.
- [x] **Step 4:** Run the integration test on a disposable PostgreSQL 16 database; expect the event and policy fanout assertions to pass.

### Task 2: Failure and sweep semantics

**Files:**
- Modify: `internal/orchestration/convergence_worker_test.go`
- Modify: `internal/orchestration/convergence_worker.go`

- [x] **Step 1:** Add tests that a malformed node ID or reconciliation error increments `retry_count`, moves `available_at` into the future, and leaves `processed_at` null; an event locked by another transaction is skipped. Add a sweep test in which a node without any outbox event receives an initial desired revision.
- [x] **Step 2:** Run those tests and observe the expected failures.
- [x] **Step 3:** Implement bounded exponential retry and `Sweep(ctx) (int,error)` over Agent nodes. Use the repository's existing idempotent digest comparison to tolerate duplicate work.
- [x] **Step 4:** Run the focused integration tests twice; verify no duplicate revisions and no lost events.

### Task 3: Process wiring and verification

**Files:**
- Modify: `cmd/control-plane/main.go`
- Modify: `README.md`
- Modify: `progress.md`

- [x] **Step 1:** Add `Run(ctx, logger)` with a 15-second event poll and 60-second sweep, and start it from `main` after database readiness. Cancellation must stop the loop cleanly; errors must be logged without stopping other work.
- [x] **Step 2:** Run complete Go tests, vet, race checks for the worker, PostgreSQL integration tests, and `git diff --check`.
- [x] **Step 3:** Commit the verified slice. Do not describe forwarding as active until Agent enrollment, transport, and apply ACK are connected.
