# Admin console implementation plan

**Goal:** Connect the existing administrator user, plan, membership, group, and node APIs to the authenticated web console without adding payment flows.

**Architecture:** Keep the existing owner console. Add one administrator route that appears only when the signed-in user has an administrator permission. Split the route into small user, plan, and resource panels. Reuse the catalog request functions for CSRF and cursor handling; keep passwords and enrollment secrets out of browser storage.

**Tech Stack:** React, TypeScript, Vite, Go REST API, PostgreSQL.

---

### Task 1: Data mapping

- [x] Write failing tests for quota and membership request conversion.
- [x] Add narrow administrator API types and conversion helpers.
- [x] Run web tests and typecheck.

### Task 2: Administrator pages

- [x] Add an RBAC-gated navigation entry.
- [x] Implement user creation and list, plan creation and list, and membership assignment.
- [x] Implement resource group and node creation and list.
- [x] Show server validation and empty states; never persist passwords or secrets.

### Task 3: Verification and release

- [ ] Run web tests, typecheck, build, Go tests and vet, and diff check.
- [x] Use a localhost mock session for desktop and mobile browser QA; keep public HTTP read-only.
- [ ] Package and deploy only after the preceding checks pass, then verify server and public IP health.
