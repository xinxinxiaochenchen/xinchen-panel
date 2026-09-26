# Subscriptions Implementation Plan

> **For agentic workers:** Use test-driven development and focused review. The user-approved system design defines this slice; no new architecture approval is needed.

**Goal:** Owners can create multiple subscriptions over existing proxy accesses, rotate/reveal tokens and export verified Mihomo or sing-box Trojan profiles.

**Architecture:** `subscriptionconfig` is a pure formatter, `subscription` owns persistence and resource filtering, and HTTP handlers use existing sessions/RBAC/CSRF. Tokens are 256-bit random values with SHA-256 lookup hashes and domain-separated AES-GCM ciphertext. Export rechecks current membership, ownership, node/line grants, Agent heartbeat, and matching applied credential on every fetch. Browser-auth-off preview exposes no subscription routes.

**Tech Stack:** Go, pgx/PostgreSQL, YAML for Mihomo, JSON for sing-box, existing AES-GCM credential cipher.

## Task 1: Pure client configuration rendering

Create `internal/subscriptionconfig/{render.go,render_test.go}`. No database or HTTP dependencies.

```go
type Target struct {
    ID, Name, LineName, Region, Server, ServerName, Password string
    Port int
}
func ValidateTemplate(template string) error
func Render(format, template string, targets []Target) ([]byte, string, error)
```

- [x] Test template placeholders `{name}`, `{line}`, `{region}`, `{index}`; reject unknown placeholders, control characters and overlong values. Default `{region} · {name}`. Disambiguate duplicate names deterministically, reserving outbound selector/direct names.
- [x] Test Mihomo YAML and sing-box JSON round-trip, TCP-only Trojan, certificate verification, no LAN listen, exact server/port/password/SNI, duplicate names, empty targets and unsupported formats. Reject missing credentials or invalid endpoint input; do not interpolate raw YAML.
- [x] Render a local mixed listener on 127.0.0.1:7890, selector with all targets, final route to selector. No routing-profile integration in this slice. Use sing-box 1.12+ local DNS resolver configuration. Verify generated configuration with available client binaries if available; otherwise state parser-validation boundary.

## Task 2: Persistence, tokens, entitlements

Create migration `000010_subscriptions.{up,down}.sql` and `internal/subscription/{model.go,token.go,repository.go,mutations.go,export.go}` plus tests.

- [x] Migration creates subscriptions (owner, unique per-owner name, token hash/ciphertext, enabled, template, timestamps) and ordered targets. Composite FKs `(subscription_id,user_id)` and `(proxy_access_id,user_id)` enforce same-owner binding. Deleting access removes target; deleting subscription removes targets. Add proxy access composite unique constraint and reverse it safely on down.
- [x] Normalize 1–100 unique UUID target IDs, name length <=100, template <=200. Test generated token canonical base64url length 43, SHA-256 hash, AES-GCM context binding via `subscription:<id>` and inability to reuse proxy context.
- [x] Create locks owner before subscription/targets, loads active frozen membership snapshot, enforces max_subscriptions and current line/group grant on every selected access. Disabled subscriptions count toward limit. Atomic insert + ordered targets + redacted audit. Transaction end rechecks membership time.
- [x] List/get are owner-scoped. Patch supports name, enabled, template and target replacement. Re-enable/target edits require current entitlement; disabling/deletion remain possible after expiry. Rotate replaces hash/ciphertext in one transaction. Reveal is owner-only; never serialize hashes or ciphertext in DTOs.
- [x] Export by token/owner uses one repeatable-read transaction, active subscription/user/membership, grant revalidation, only enabled access with matching applied Agent credential, online heartbeat <=45 seconds, node/group/line enabled and proxy capability. Decrypt credential only after filtering. Return unavailable when no eligible targets; never fall back to direct. Recheck membership expiry before return.
- [x] Test real PostgreSQL lifecycle, target ownership, quota/concurrent creation, token rotation, deletion cascade, frozen grant removal, expiry, offline Agent and rotated-but-unapplied credentials. Verify migration up/down on isolated PostgreSQL 16.

## Task 3: REST, token redaction, wiring

Create `internal/platform/httpapi/subscriptions.go` and tests; update `server.go`, `web.go`, `cmd/control-plane/main.go`, OpenAPI.

- [x] Existing subscription read/write RBAC gates all management endpoints; writes require CSRF. GET/POST collection, GET/PATCH/DELETE item, POST token-rotation, GET url/preview by format. URL response is an origin-independent path so untrusted Host/Forwarded headers cannot inject a URL origin.
- [x] `/sub/{token}/{format}` uses bearer-token authorization without browser cookie, bounded concurrency plus known-token/global limiter, no-store headers and no referrer. Auth-disabled mode has no route. Validate formats before DB work. Hide missing/disabled/expired token as 404; no eligible configured target returns 503.
- [x] Middleware redacts the entire `/sub/` path for access logs, including malformed/unknown token requests. Static handler delegates `/sub/` to API instead of SPA fallback. Test these boundaries and reset revocation.
- [x] Add contracts and limitations to README/OpenAPI. Test HTTP auth/CSRF/error handling, token redaction and disabled preview routes.

## Task 4: Verify and release

- [x] Run full Go suite, targeted race, vet, OpenAPI parse, frontend build, diff check and code review. Build Linux release and test database migration in isolated remote DB.
- [ ] Back up formal DB, deploy using Termark, confirm health and public HTTP subscription/auth routes remain closed. Record deployment and remaining routing/quota/UI gaps.

Commands use `/private/tmp/network-control-plane-go/go/bin/go` with `GOCACHE=/private/tmp/network-control-plane-gocache GOPATH=/private/tmp/network-control-plane-gopath`. Socket tests require elevated local execution. Remote execution uses the saved Termark asset `yYnZwRDjhzeD2Fv3`; no SSH/scp.
