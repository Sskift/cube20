# cube run Recovery Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden `cube run` around bare Codex bypasses, quota state clarity, missing telemetry, resume failures, expired leases, and reset-time refresh without changing claim/swap thresholds.

**Architecture:** Keep the current lease and load-balancing model. Add derived runtime status fields, a claim pre-refresh pass for stale quota, a heartbeat telemetry marker, safer swap cleanup, and a local cloud status diagnostic that warns about unmanaged bare `codex` use.

**Tech Stack:** Go CLI and HTTP server, manager state/Postgres abstractions, Go tests with `httptest`, existing React types for status display.

---

### Task 1: Runtime Status and Claim Refresh

**Files:**
- Modify: `internal/manager/loadbalance.go`
- Modify: `internal/manager/lease.go`
- Modify: `internal/manager/manager_test.go`

- [x] Add tests for `quota_cooldown`, `refresh_needed`, and stale reset pre-refresh.
- [x] Add `RuntimeState` and `RuntimeReason` to `LoadBalanceAccount`.
- [x] Add a derived runtime-state helper backed by existing eligibility reasons.
- [x] Add a pre-refresh pass in `ClaimLease` for accounts whose reset time has passed.
- [x] Verify manager tests.

### Task 2: Heartbeat Telemetry Marker

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/server_test.go`
- Modify: `cmd/cube/main.go`

- [x] Add a failing heartbeat test proving empty quota windows return `quotaTelemetryMissing: true`.
- [x] Add the response field without overwriting existing quota cache.
- [x] Make the CLI warn once when heartbeat reports missing telemetry.
- [x] Verify web and CLI tests.

### Task 3: Safe Swap Failure Cleanup

**Files:**
- Modify: `cmd/cube/main.go`
- Modify: `cmd/cube/lifecycle_test.go`

- [x] Add tests for releasing a newly claimed lease when resume preparation fails.
- [x] Extract swap planning/execution helper around claim/write/release.
- [x] Ensure old lease final cleanup is preserved.
- [x] Print deterministic recovery hints.
- [x] Verify lifecycle tests.

### Task 4: Bare Codex Diagnostic

**Files:**
- Modify: `cmd/cube/main.go`
- Modify: `internal/web/server.go`
- Add: `cmd/cube/cloud_status_test.go`

- [x] Add an admin/device-safe endpoint or reuse `/api/me` data to identify managed account matches.
- [x] Add `cube cloud status` to print device identity and warn if live auth matches a managed account.
- [x] Add tests for summary generation and redaction.
- [x] Verify CLI tests.

### Task 5: Frontend/API Type Clarity

**Files:**
- Modify: `web/src/types.ts`
- Modify: load-balancer view files under `web/src`

- [x] Add runtime-state fields to TypeScript types.
- [x] Surface runtime state text in the load balancer account rows.
- [x] Verify `npm run lint` and `npm run build`.

### Task 6: Full Verification and Deploy

**Files:**
- Modify: `web/dist` after frontend build

- [x] Run `go test ./... -count=1`.
- [x] Run `go test ./... -race -count=1`.
- [x] Run `go vet ./...`.
- [x] Run `go build ./...`.
- [x] Run `cd web && npm run lint && npm run build`.
- [x] Build Linux binary, canary on devbox, deploy, smoke `/healthz` and `/readyz`.
- [ ] Commit and push branch `cube20-init-43981`.
