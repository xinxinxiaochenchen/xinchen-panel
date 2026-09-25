# Agent Direct Forward Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An isolated Go Agent runtime applies versioned TCP/UDP forwarding snapshots, preserves the last working snapshot on bind or validation failure, and never dials a non-public resolved destination.

**Architecture:** `internal/agentruntime` owns snapshot validation, listener lifecycle, destination resolution and TCP/UDP relaying. Its input is a trusted, compiled node snapshot; it still validates ports, duplicate listeners and destination IPs before use. `Apply` stages all new listeners before changing the live map. Existing sockets for unchanged protocol/port pairs remain bound; their target switches only after staging succeeds. The runtime is transport-independent so a later mTLS Agent stream can call it without embedding WebSocket logic in the data plane. This slice does not yet install the Agent or claim control-plane convergence.

**Tech Stack:** Go 1.27.1 standard library, `net`, `net/netip`, `sync`, existing `forward.PublicIP` policy.

## Behavioral contract

- `Snapshot{Revision, Rules}` has strictly increasing positive revisions. Each enabled `Rule` has a stable ID, ingress port 1024–65535, protocol TCP/UDP/BOTH, target DNS name or public IP literal and target port 1–65535. Disabled rules are absent from the effective listener set.
- Duplicate physical listener keys `(protocol, ingress_port)` fail validation. BOTH expands into TCP and UDP, and either both bind or neither becomes active. A failed `Apply` returns an error and leaves the previous revision and listeners unchanged.
- TCP resolves the target for each accepted connection, keeps only public IPs, and dials a selected numeric IP directly. It relays both directions with half-close support and a bounded drain period. Disabling a listener or changing its target closes active streams.
- UDP maintains bounded per-client associations with idle expiry. It resolves before opening each association, dials a numeric public IP, and sends replies only to the matching client. Changing a UDP listener's target closes its associations. Limits are explicit and configurable.
- Runtime has a configurable bind address and injectable resolver/dial functions for deterministic loopback tests. Production defaults use the system resolver and `net.Dialer`. Test injection cannot bypass the public-IP filter. `Close` is idempotent, stops listeners and waits for listener loops.

## Task 1: Snapshot validation

**Files:** `internal/agentruntime/types.go`, `internal/agentruntime/types_test.go`.

- [x] Write table tests for positive revision, valid TCP/UDP/BOTH, disabled exclusion, duplicate key, invalid port/host and private IP. Run `go test ./internal/agentruntime -run TestValidateSnapshot -count=1` and confirm failure before implementation.
- [x] Implement `ValidateSnapshot(Snapshot) (map[listenerKey]target, error)` and `ResolvePublic(ctx, host, resolver) (netip.Addr,error)`. Reject any resolved non-public address and select only public candidates. Run the focused tests to green.

## Task 2: Atomic listener lifecycle

**Files:** `internal/agentruntime/runtime.go`, `internal/agentruntime/runtime_test.go`.

- [x] Write real loopback tests: apply revision 1; block a new port; apply revision 2 and assert revision 1 still accepts; free the port, apply revision 2, and assert both TCP and UDP listeners are staged. Verify stale/equal revisions fail. Run the focused test and observe the expected failure.
- [x] Implement `New(Options)`, `Apply(context.Context, Snapshot)`, `Revision()`, and `Close()` with staged socket binding, atomic target swap, rollback on bind failure, and idempotent shutdown. Run the focused test to green.

## Task 3: TCP and UDP relays

**Files:** `internal/agentruntime/tcp.go`, `internal/agentruntime/udp.go`, `internal/agentruntime/relay_test.go`.

- [x] Write real loopback echo tests for TCP and UDP with a resolver returning a public test address and a dialer mapping that numeric address to a loopback echo server. Assert uploaded and downloaded payloads match. Add tests where a resolver returns only private addresses and the dialer is never called. Run tests and confirm red.
- [x] Implement per-connection DNS resolution, public-IP filtering, TCP bidirectional copy, bounded UDP associations and idle expiry. When a UDP target changes, close old associations. Run tests to green, including `-race` for the runtime package.

## Task 4: Documentation and verification

**Files:** `README.md`, `task_plan.md`, `progress.md`, `findings.md`.

- [x] Document the runtime's snapshot contract and state clearly that Agent enrollment, mTLS transport and production deployment are still pending.
- [x] Run full `go test ./... -count=1`, `go vet ./...`, `go test -race ./internal/agentruntime -count=1`, and `git diff --check`; inspect the diff and commit this isolated slice.
