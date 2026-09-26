# Agent TLS IP Ingress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Allow a deliberate, TLS-protected Agent listener on a public IP while keeping the current preview closed by default.

**Architecture:** Keep the dedicated Agent HTTPS/mTLS handler. Require an explicit opt-in before its listener can bind outside loopback. Add an optional Compose override that mounts private certificate files and publishes only the Agent port; the base preview remains unchanged.

**Tech Stack:** Go 1.27, Docker Compose, existing Agent identity and TLS packages.

---

### Task 1: Explicit public-bind gate

**Files:** `internal/platform/config/config_test.go`, `internal/platform/config/config.go`

- [x] Add a test with complete Agent certificate path fixtures and `0.0.0.0:18443`: reject by default, accept only when `CONTROL_AGENT_PUBLIC_TLS_ENABLED=true`, reject malformed flag, reject opt-in without complete TLS configuration.
- [x] Run `go test ./internal/platform/config -run AgentTLS -count=1` and confirm the acceptance assertion fails.
- [x] Parse the new flag and update listener host validation; permit loopback by default and an IP wildcard or literal IP only with opt-in.
- [x] Rerun focused tests and `go test ./internal/platform/config -count=1`.

### Task 2: Optional deployment overlay

**Files:** `deployments/compose/compose.agent-tls.yaml`, `docs/deployment/private-preview.md`

- [x] Add an overlay for the API service with `CONTROL_AGENT_PUBLIC_TLS_ENABLED=true`, `CONTROL_AGENT_TLS_ADDR=0.0.0.0:18443`, certificate paths under a read-only mount, and a host-side bind IP defaulting to loopback.
- [x] Document required CA/server certificate SAN, private file ownership for UID 65532, port choice, and the command to validate the combined Compose configuration. Keep the base Compose file and running server unchanged.
- [x] Parse the YAML and validate the merged Compose configuration in an isolated environment.

### Task 3: Verification

- [x] Run complete Go tests, vet, front-end tests/typecheck/build, OpenAPI parse, and `git diff --check`.
- [x] Commit the focused change and report the remaining need for certificate provisioning and first Agent host selection.

Verified 2026-09-26: focused test failed before implementation at the explicit opt-in acceptance assertion, then passed. Complete Go tests, vet, 20 web tests, typecheck/build, YAML parse and diff check passed. The optional Compose overlay passed remote `docker compose config --quiet`; filtered config showed host 127.0.0.1 by default and 0.0.0.0 only with explicit bind setting. Production containers were not changed.
