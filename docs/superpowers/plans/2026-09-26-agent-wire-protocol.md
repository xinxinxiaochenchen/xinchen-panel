# Agent Wire Protocol Foundation Implementation Plan

> **For agentic workers:** Use test-driven development for each behavior before adding production code. This is one bounded slice of the approved Agent architecture; transport and enrollment remain separate slices.

**Goal:** Define a strict, bounded JSON envelope shared by Agent and control plane, plus typed forwarding snapshot and result payloads.

**Architecture:** `internal/agentproto` owns wire schemas and validation, with no database or HTTP dependencies. Each message has a protocol version, unique message ID, certificate-bound node ID, type, timestamp and typed payload. A complete forward snapshot includes the persisted executable digest, revision and expiration. Decoding rejects unknown fields, duplicate or trailing JSON, oversized frames and unsupported protocol versions. The transport later binds the envelope node ID to its mTLS certificate.

**Tech Stack:** Go standard library JSON and IO; existing forwarding rule type.

## Task 1: Envelope codec

**Files:** `internal/agentproto/envelope.go`, `internal/agentproto/envelope_test.go`.

- [x] Write failing tests for round trip, missing IDs/payload, unsupported version/type, unknown fields, trailing JSON and frame above 1 MiB.
- [x] Implement strict encode/decode with a 1 MiB limit and validation.

## Task 2: Typed forwarding payloads

**Files:** `internal/agentproto/payload.go`, `internal/agentproto/payload_test.go`.

- [x] Write failing tests for snapshot revision/digest/expiry, result status/error bounds, and digest mismatch.
- [x] Implement typed payload parsing and digest verification using the canonical executable configuration shape.

## Task 3: Verification

- [x] Run package and complete Go tests, vet and diff check, then commit the independent protocol slice.
- [x] Keep the public preview unchanged until enrollment, mTLS and an Agent process can use this protocol.
