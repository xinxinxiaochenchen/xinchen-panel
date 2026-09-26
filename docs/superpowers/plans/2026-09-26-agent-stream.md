# Agent Stream and Forward Execution Implementation Plan

> **For agentic workers:** Use test-driven development for each behavior before adding production code. This plan continues the approved Agent architecture and keeps the public HTTP preview unchanged until secure transport is ready.

**Goal:** Connect an enrolled Go Agent to the control plane over mTLS WebSocket so it receives complete forward snapshots, applies them through `agentruntime`, and reports heartbeats and configuration results.

**Architecture:** `agentproto` validates typed hello and heartbeat payloads. `orchestration` persists online observations and configuration results. A dedicated TLS listener authenticates the client certificate before WebSocket upgrade and periodically rechecks revocation; one active stream owns each node in a process. `cmd/agent` owns the outbound connection, local forward runtime and durable applied snapshot. This slice supports the existing direct TCP/UDP forwarding model; proxy connections, usage accounting and remote Agent bootstrap remain later slices.

**Tech Stack:** Go 1.27, `github.com/coder/websocket`, pgx/v5, PostgreSQL 16, mTLS, Docker Compose.

---

### Task 1: Message payloads

**Files:** `internal/agentproto/session_test.go`, `internal/agentproto/session.go`.

- [x] Test strict hello and heartbeat decoding, bounds and malformed payload rejection; observe failure before implementation.
- [x] Add typed payloads with `DecodeHello` and `DecodeHeartbeat`; keep strict duplicate and unknown field checks from the existing decoder.

### Task 2: Agent presence

**Files:** `migrations/000007_agent_metrics.*.sql`, `internal/orchestration/agent_presence_test.go`, `internal/orchestration/agent_presence.go`.

- [x] Test online transition, heartbeat metrics, stale offline transition and revoked rejection in an isolated PostgreSQL 16 database; observe failure.
- [x] Implement transactional repository methods and latest metrics storage. The database remains authoritative for status; in-memory stream ownership is only a local connection guard.

### Task 3: Secure stream server

**Files:** `internal/platform/httpapi/agent_stream_test.go`, `internal/platform/httpapi/agent_stream.go`, `cmd/control-plane/agent_server.go`.

- [x] Test TLS client certificate requirement, node ID binding, hello, snapshot delivery, result persistence and revocation; observe failure.
- [x] Mount `/api/v1/agent/stream` only on the dedicated TLS listener. Authenticate before upgrade, cap frames and timeouts, recheck authorization, send desired snapshots after hello and when revisions change, and persist results.

### Task 4: Outbound Agent

**Files:** `internal/agentclient/client_test.go`, `internal/agentclient/client.go`, `internal/agentclient/state.go`, `cmd/agent/main.go`.

- [x] Test TLS dial with trusted server CA and client certificate, hello, valid snapshot application, ACK, reconnect and 0600 state; observe failure.
- [x] Implement reconnect with bounded backoff, atomic 0600 snapshot state and graceful runtime shutdown. The Agent accepts only `wss://` with a trusted CA and verified server name/IP.

### Task 5: Verify and deploy

**Files:** `api/openapi/control-plane.yaml`, `README.md`, `docs/deployment/private-preview.md`, `progress.md`, `deployments/compose/compose.yaml`.

- [x] Run focused and full Go tests, race tests, vet, OpenAPI validation and frontend build. Run migration up/down and stream integration in an isolated PostgreSQL 16 database.
- [x] Build Linux amd64 binaries, back up the production database, deploy a new release to `us dmit`, and verify public HTTP stays read-only while Agent TLS is not exposed until configured. Record the exact release and remaining MVP gaps.
