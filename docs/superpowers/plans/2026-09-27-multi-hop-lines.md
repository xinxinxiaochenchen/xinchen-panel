# TCP Multi-Hop Lines Implementation Plan

Status: In progress under the ongoing authorization to complete development. No alternate transport preference was received; proceed with the recommended direct mTLS transport. Disabled topology, handshake validation, port reservations and mTLS identity foundations are implemented; executable integration remains pending.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add authorized TCP multi-hop line compilation and Agent-to-Agent relay execution while preserving existing single-hop forwarding and proxy behavior.

**Architecture:** The control plane validates ordered `line_hops` and compiles a per-Agent relay snapshot. Entry Agents terminate the existing Trojan connection and open an authenticated TLS relay to the next hop; relay Agents only forward an authorized route frame; egress Agents perform the existing public-target validation and dial. Route secrets are generated per line edge and carried only in the two endpoint snapshots. Metering remains at the user ingress.

**Tech Stack:** Go, PostgreSQL 16, existing `coder/websocket` Agent stream, TLS 1.3, `agentruntime`, `agentproto`.

---

### Task 1: Model and validate multi-hop line topology

**Files:**
- Modify: `internal/catalog/line_model.go`
- Modify: `internal/catalog/line_repository.go`
- Modify: `internal/catalog/line_mutations.go`
- Modify: `internal/catalog/line_repository_test.go`
- Create: `migrations/000020_node_relay_port.up.sql`
- Create: `migrations/000020_node_relay_port.down.sql`
- Modify: `api/openapi/control-plane.yaml`

- [x] Add a typed hop input with contiguous positions, unique nodes, exactly one ingress and egress, and relay roles only in the middle; multi-hop remains disabled draft only.
- [x] Keep existing one-node requests compatible by translating the current `node_id` input to one egress hop.
- [x] Add nullable node relay port with atomic TCP/UDP reservations and cross-kind TCP endpoint uniqueness; use the existing node host as advertised address, while bind host remains Agent-local. Disabled nodes retain reservations.
- [x] Lock all draft nodes and resource groups in sorted UUID order before checking capability, enabled state, group grants, and `max_hops`.
- [x] Test two-hop and three-hop authorization, duplicate nodes, role mismatch, disabled relay, missing forward capability, and `max_hops=2` allowing one or two hops.

### Task 2: Compile per-Agent TCP relay snapshots

**Files:**
- Modify: `internal/agentruntime/types.go`
- Modify: `internal/agentproto/payload.go`
- Modify: `internal/orchestration/revision_repository.go`
- Create: `internal/orchestration/multi_hop_snapshot.go`
- Create: `internal/orchestration/multi_hop_snapshot_test.go`
- Modify: `internal/orchestration/proxy_repository.go`

- [ ] Define `RelayConfig` with line ID, generation, local role, previous node, next node, route secret, and target policy mode.
- [ ] Compile only lines whose every Agent is online, capable, and has the same desired generation; omit incomplete lines with diagnostics.
- [ ] Generate a random 32-byte edge secret once per line generation and include it only in adjacent endpoint configs.
- [ ] Preserve the existing canonical digest and make unknown relay fields a hard decode error.
- [ ] Test deterministic ordering, secret non-reuse across generations, incomplete ACK gating, revoked membership, and single-hop digest compatibility.

### Task 3: Agent-to-Agent TCP relay protocol

**Files:**
- Create: `internal/agentrelay/protocol.go`
- Create: `internal/agentrelay/protocol_test.go`
- Modify: `internal/agentproto/envelope.go`
- Modify: `internal/agentproto/payload.go`

- [x] Add a bounded length-prefixed `OPEN`, `OPEN_OK`, and `OPEN_ERR` frame with line ID, connection ID, generation, target host/port, and edge proof.
- [x] Reject oversized frames, private IP literals, stale generations, invalid ports, replayed connection IDs, and constant-time secret mismatches. DNS targets still require public IP resolution at runtime.
- [ ] Keep route secrets and target details out of application logs and audit payloads.

### Task 4: Agent relay runtime

**Files:**
- Create: `internal/agentruntime/relay.go`
- Create: `internal/agentruntime/relay_test.go`
- Modify: `internal/agentruntime/proxy.go`
- Modify: `internal/agentruntime/runtime.go`
- Modify: `cmd/agent/config.go`
- Modify: `cmd/agent/main.go`

- [ ] Add a dedicated mTLS relay listener and outbound dialer using the existing Agent CA and node identity verification.
- [ ] Route each OPEN only through the applied local `RelayConfig`; relay Agents cannot accept a caller-selected next hop.
- [ ] At egress, reuse public DNS/IP validation before dialing and return bounded error codes.
- [ ] Close all downstream connections when a relay config is revoked or its generation changes.
- [ ] Test unauthorized source certificates, wrong proofs, stale generations, public-target enforcement, and bidirectional TCP payloads.

### Task 5: Weighted line selection and ingress-only metering

**Files:**
- Create: `internal/orchestration/line_selection.go`
- Create: `internal/orchestration/line_selection_test.go`
- Modify: `internal/agentruntime/proxy.go`
- Modify: `internal/billing/admission.go`

- [x] Add and test a pure selector that orders healthy candidates by priority and stable connection-ID weighted rank. Runtime wiring remains below.
- [ ] Use the selector only after all hop ACK and health checks when opening a new connection.
- [ ] Retry another healthy line only before opening a logical metered connection; keep the original line and multiplier fixed after admission.
- [ ] Test priority, weighted distribution, stable retries, unhealthy ACK gating, and one ledger connection for a multi-hop stream.

### Task 6: Integration, documentation, and release

**Files:**
- Create: `internal/agentrelay/integration_test.go`
- Modify: `api/openapi/control-plane.yaml`
- Modify: `README.md`
- Modify: `docs/deployment/private-preview.md`
- Modify: `progress.md`

- [ ] Run focused tests, full Go tests with loopback permissions, race tests for relay state, `go vet ./...`, OpenAPI parsing, and frontend build.
- [ ] Run migrations 1–20 up/down/up in an isolated PostgreSQL 16 database.
- [ ] Run a three-Agent TCP integration test with one ingress, one relay, and one egress and assert ingress-only usage accounting.
- [ ] Build and deploy through Termark only after all verification passes; retain a database backup and verify public HTTP remains read-only.
