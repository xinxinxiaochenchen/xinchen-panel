# Agent Enrollment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give an Agent a one-time enrollment credential and a certificate bound to one node without exposing credentials on the public HTTP preview.

**Architecture:** `internal/agentidentity` separates CA signing from PostgreSQL token consumption. The database stores only token digests and the currently accepted certificate fingerprint. An isolated TLS API and the Agent stream will use these primitives in subsequent tasks; this plan first establishes their verified domain behavior.

**Tech Stack:** Go standard library X.509/Ed25519, pgx/v5, PostgreSQL 16, migration 6.

---

### Task 1: Certificate identity

**Files:** `internal/agentidentity/certificate_test.go`, `internal/agentidentity/certificate.go`.

- [x] Write tests for CA validation, Ed25519 CSR proof of possession, node URI override, client-only EKU, 24-hour validity, fingerprint, and rejection of invalid keys or CSR. Run focused tests and observe expected missing API failure.
- [x] Implement `NewIssuer(caCertPEM, caKeyPEM)` and `IssueClientCertificate(csrPEM,nodeID,now)` with no database or HTTP dependency. Run focused tests to green.

### Task 2: One-time token persistence

**Files:** `migrations/000006_agent_enrollment.up.sql`, `.down.sql`, `internal/agentidentity/enrollment_test.go`, `internal/agentidentity/enrollment.go`.

- [x] Write PostgreSQL tests covering one-time token creation, expiry, malformed CSR preserving the token, a successful certificate + stored fingerprint, replay rejection and concurrent consumption. Run against a disposable PostgreSQL 16 database and observe expected failure.
- [x] Add migration 6 and service methods `CreateToken` and `Enroll`, with token hash only in storage, 10-minute expiry, node locking and one transaction for consumption plus fingerprint update. Run integration tests to green.

### Task 3: Documentation and verification

**Files:** `README.md`, `progress.md`, `api/openapi/control-plane.yaml`.

- [ ] Document the credential boundary and endpoint contract without exposing it on current public HTTP. Run all Go tests, vet, race checks, migration up/down in independent PostgreSQL 16 and diff check.
- [ ] Commit the verified slice. Proceed to mTLS endpoint wiring and Agent process; do not mark forwarding active from this slice alone.
