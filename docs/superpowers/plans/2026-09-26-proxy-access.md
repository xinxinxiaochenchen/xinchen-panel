# Trojan Proxy Access Implementation Plan

> **For agentic workers:** Execute this plan with test-driven development. The approved system design in `docs/superpowers/specs/2026-09-25-network-control-plane-design.md` governs product behavior.

**Goal:** Let an authorized user create and manage a single-hop Trojan proxy access, and deliver its executable credential to the assigned Agent so clients can connect through TLS.

**Architecture:** A `proxyaccess` domain owns access validation, generated credentials, encryption, rotation, persistence and audit. It binds an access to one usable line whose sole egress node has `proxy` capability and lies within the current membership snapshot. Configuration convergence compiles hash-only Trojan credentials for each node; the Agent runs a TLS Trojan adapter with explicit certificate configuration. Secret plaintext is available only to the owner through authorized API and later subscription rendering. The public HTTP preview keeps browser auth and Agent TLS disabled.

**Tech Stack:** Go 1.27, pgx/v5, PostgreSQL 16, AES-256-GCM, SHA-224 Trojan credential hash, REST/OpenAPI, mTLS Agent WebSocket.

---

### Task 1: Credential primitive and migration

**Files:** `internal/proxyaccess/credential_test.go`, `internal/proxyaccess/credential.go`, `migrations/000008_proxy_accesses.up.sql`, `migrations/000008_proxy_accesses.down.sql`, `internal/platform/db/migrate_test.go`.

- [x] Test generated token uniqueness/length, AES-GCM round trip, wrong-key/tamper rejection, Trojan SHA-224 digest, and key parsing.
- [x] Implement a strict 32-byte key parser and authenticated encryption with a fresh nonce per ciphertext. Ciphertext binds `access_id` and `user_id`.
- [x] Add owner/line foreign keys, unique per-owner name, bounded status, credential hash uniqueness, ciphertext, audit-friendly timestamps and owner pagination index. Verify migration up/down in a fresh PostgreSQL 16 database.

### Task 2: Access domain and repository

**Files:** `internal/proxyaccess/model_test.go`, `model.go`, `repository_test.go`, `repository.go`.

- [x] Test name/line validation and safe API DTO that never includes hash/ciphertext.
- [x] Test creation, pagination, get, enable/disable, delete and credential rotation in PostgreSQL 16. Include cross-user ownership, expired membership, disabled/unauthorized line and concurrent same-name creation.
- [x] Use owner row lock for mutations, a current membership snapshot, line/node/group revalidation, transactional audit and outbox. Return stable domain errors.

### Task 3: REST, configuration and data plane

**Files:** `internal/platform/httpapi/proxy_access*.go`, `cmd/control-plane/main.go`, `internal/orchestration/*`, `internal/agentproto/*`, `internal/agentruntime/*`, `cmd/agent/*`, `api/openapi/control-plane.yaml`.

- [x] Test REST ownership, RBAC, CSRF, no-cache credential reads, rotation and error mapping.
- [x] Test node-scoped proxy snapshot compilation, ACL revocation, digest and ACK/NACK behavior; extend protocol with proxy configuration.
- [x] Test TLS Trojan CONNECT against a real local socket using a generated cert; reject unknown credentials, unsupported commands, unauthorized targets and expired membership/lease.
- [x] Wire Agent runtime and private certificate configuration. Deliver encrypted credentials only to the owner; transmit hash-only proxy config over mTLS. The Agent fails closed on disconnect or expired execution lease.
- [ ] Package and deploy this phase without exposing browser secrets on the pure HTTP preview.

### Task 4: Release verification

- [x] Run full Go tests, race tests, vet, OpenAPI parse and frontend build.
- [x] Test migration up/down, ownership, concurrent lock order, proxy revision compilation, rotation and ACK/NACK in isolated PostgreSQL 16 databases.
- [x] Test mTLS control-plane stream and real Agent runtime locally: apply proxy config, relay TLS Trojan TCP payload, ACK, revoke, disconnect the existing client and persist the new revision.
- [ ] Back up the formal database, deploy via Termark, verify health and public HTTP auth closure, and record exact remaining MVP gaps.

## Release boundaries

- This adapter supports Trojan TCP CONNECT with public destinations only. Trojan UDP, multi-hop proxying, usage accounting and quota leases remain outside this phase.
- Each proxy node uses a TLS listener on its configured proxy port, including 443 when the Agent has bind permission. Its certificate and private key are configured on the Agent separately from the control-channel mTLS client identity.
- Config snapshots have a five-minute execution lease, renewed every minute. Membership expiry is also enforced locally for each proxy session. The execution lease is distinct from the planned billable-byte quota lease.
- The public HTTP preview keeps browser authentication closed and has no live user/Agent credentials. Actual server data-plane activation follows quota enforcement and a controlled deployment test.
