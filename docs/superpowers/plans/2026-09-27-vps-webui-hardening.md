# VPS WebUI Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Finish the VPS-facing WebUI delivery slice by making the new secure deployment overlay and migration 24 behavior reviewable and repeatable.

**Architecture:** Keep the browser UI served by the Go control plane and deploy it with Docker Compose. Keep HTTP IP preview read-only; enable browser authentication only behind trusted HTTPS with a file-mounted proxy credential key. Validate migration 24 against PostgreSQL semantics through the existing migration integration harness.

**Tech Stack:** Go, PostgreSQL 16, Docker Compose, React/Vite WebUI, SQL migrations.

---

### Task 1: Validate the pending deployment documentation and compose overlay

**Files:**
- Modify: `README.md`
- Create: `deployments/compose/compose.proxy-secrets.yaml`
- Create: `docs/deployment/vps-webui.md`

- [x] **Step 1: Inspect the diff and YAML shape**

Run:

```bash
git diff --check
git diff -- README.md deployments/compose/compose.proxy-secrets.yaml docs/deployment/vps-webui.md
```

Expected: no whitespace errors; the overlay mounts a read-only key file and does not contain secrets.

- [x] **Step 2: Check compose variable references without Docker**

Run:

```bash
rg -n "CONTROL_PROXY_CREDENTIAL_KEY_FILE|compose.proxy-secrets|https|nginx|readonly|read-only" README.md docs/deployment/vps-webui.md deployments/compose/compose.proxy-secrets.yaml
```

Expected: the documented path and environment variable are identical in all three files.

- [ ] **Step 3: Commit the deployment slice**

```bash
git add README.md deployments/compose/compose.proxy-secrets.yaml docs/deployment/vps-webui.md docs/superpowers/plans/2026-09-27-vps-webui-hardening.md
git commit -m "docs: document secure VPS WebUI deployment"
```

### Task 2: Add a PostgreSQL migration 24 integration regression

**Files:**
- Modify: `internal/platform/db/migrate_test.go`
- Test: `migrations/000024_forward_rule_lines.up.sql`, `migrations/000024_forward_rule_lines.down.sql`
- Modify: `progress.md`

- [x] **Step 1: Write the failing integration assertion**

Add a test that creates a temporary database, applies migrations 1 through 24, inserts a forward rule with only `line_id`, verifies it succeeds, verifies a rule with both `line_id` and `target_node_id` fails with the named check constraint, deletes the temporary rows, applies migration 24 down, and verifies direct-only rows still succeed while line-bound rows fail.

- [ ] **Step 2: Run the focused test before implementation**

```bash
go test ./internal/platform/db -run TestMigration24ForwardRuleLineConstraint -count=1 -v
```

Expected: FAIL when no PostgreSQL test DSN is configured; with `CONTROL_TEST_DATABASE_URL` configured the test must initially fail only if the assertion is not implemented.

- [x] **Step 3: Implement only the test harness helper needed to isolate migration 24**

Reuse the existing migration loader and transaction helpers. Do not change production SQL unless the real PostgreSQL run exposes a syntax or constraint issue.

- [ ] **Step 4: Run the focused test and the complete database package**

```bash
go test ./internal/platform/db -run TestMigration24ForwardRuleLineConstraint -count=1 -v
go test ./internal/platform/db -count=1
```

Expected: the focused test passes when `CONTROL_TEST_DATABASE_URL` points to PostgreSQL 16; the package remains green when the DSN is absent by following the repository's existing skip convention.

- [ ] **Step 5: Record the evidence**

Append the PostgreSQL 16 result, migration version, and cleanup result to `progress.md`.

### Task 3: Verify the whole local delivery gate

**Files:**
- No production file changes.

- [ ] **Step 1: Run Go verification**

```bash
go test ./... -count=1
go vet ./...
git diff --check
```

- [x] **Step 2: Run WebUI verification**

```bash
npm --prefix apps/web test
npm --prefix apps/web run build
```

- [x] **Step 3: Review the requirement boundary**

Confirm the docs state that HTTPS and browser authentication are required before write operations or secret-bearing subscriptions, and that the IP preview remains read-only.
