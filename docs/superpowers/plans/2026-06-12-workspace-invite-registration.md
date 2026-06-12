# Workspace Invite Registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build workspace invite links so new users join a workspace by registering from an invite page, then create their own device token.

**Architecture:** Add invite records to manager state with token hashes and additive Postgres storage. Expose workspace-admin invite management APIs plus public preview/register/join APIs. Replace the Workspaces add-member UI with invite-link controls and add an `/invite/:token` registration/device flow.

**Tech Stack:** Go HTTP handlers and manager state, Postgres via `lib/pq`, React/TypeScript/Vite/HeroUI, Go tests with `httptest`, frontend validation through lint/build.

---

### Task 1: Backend Invite Manager

**Files:**
- Modify: `internal/manager/manager.go`
- Create: `internal/manager/invites.go`
- Test: `internal/manager/invites_test.go`

- [ ] **Step 1: Write failing manager tests**

Create tests covering invite creation, public lookup, registration consumption, revocation, expiration, and idempotent logged-in join.

Run: `go test ./internal/manager -run 'TestWorkspaceInvite' -count=1`

Expected: FAIL because invite APIs/types do not exist.

- [ ] **Step 2: Add invite state types**

Add `WorkspaceInvite`, `WorkspaceInviteView`, `WorkspaceInviteCreated`, and `InvitePreview` to `internal/manager/manager.go`. Add `Invites []WorkspaceInvite` to `State` and initialize it in `normalizeState`.

- [ ] **Step 3: Implement invite manager methods**

Create `internal/manager/invites.go` with:

```go
func (m *Manager) CreateWorkspaceInvite(workspaceID, createdBy string, role WorkspaceRole, ttl time.Duration) (WorkspaceInviteCreated, error)
func (m *Manager) ListWorkspaceInvites(workspaceID string) ([]WorkspaceInviteView, error)
func (m *Manager) RevokeWorkspaceInvite(workspaceID, inviteID string) error
func (m *Manager) InvitePreview(token string) (InvitePreview, error)
func (m *Manager) RegisterWithInvite(token, username, password string) (UserView, string, []WorkspaceMembershipView, error)
func (m *Manager) JoinWithInvite(token, userID string) ([]WorkspaceMembershipView, error)
```

Use `hashToken` for invite token hashes and a new `generateInviteToken()` returning `cube_inv_...`.

- [ ] **Step 4: Verify manager tests pass**

Run: `go test ./internal/manager -run 'TestWorkspaceInvite' -count=1`

Expected: PASS.

### Task 2: Postgres Persistence

**Files:**
- Modify: `internal/manager/store_postgres.go`
- Test: existing manager/web tests indirectly, plus build.

- [ ] **Step 1: Extend schema**

Add `CREATE TABLE IF NOT EXISTS cube_workspace_invites (...)` with columns matching `WorkspaceInvite`, plus indexes on `workspace_id` and `token_hash`.

- [ ] **Step 2: Load and save invites**

Update `loadPostgresState` to read invite rows into `state.Invites`. Update `savePostgresState` to upsert invites with monotonic usage fields and preserve revocation.

- [ ] **Step 3: Add targeted revoke helper if needed**

If generic save cannot clear or revoke correctly, add `revokePostgresWorkspaceInvite(workspaceID, inviteID string)` and call it from manager revoke.

- [ ] **Step 4: Run storage-facing checks**

Run: `go test ./internal/manager -count=1`

Expected: PASS.

### Task 3: Invite HTTP API

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/workspace_handlers.go`
- Create: `internal/web/invite_handlers.go`
- Test: `internal/web/workspace_invites_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Cover:

- workspace admin creates an invite;
- non-admin cannot create/list/revoke;
- public preview returns safe workspace data;
- invite registration sets cookie, creates user, and returns workspace membership;
- logged-in join is idempotent;
- revoked/expired invite rejects preview/register/join;
- device created after invite registration sees joined workspace in `/api/me`.

Run: `go test ./internal/web -run 'TestWorkspaceInvite' -count=1`

Expected: FAIL because routes do not exist.

- [ ] **Step 2: Register routes**

Add:

```go
mux.HandleFunc("/api/invites/", s.handleInviteAction)
```

Extend `/api/workspaces/{id}/...` routing for:

- `GET/POST /api/workspaces/{id}/invites`
- `DELETE /api/workspaces/{id}/invites/{inviteId}`

- [ ] **Step 3: Implement handlers**

Create `internal/web/invite_handlers.go` for public preview/register/join. Use existing session cookie helpers, login limiter, and `withSessionAuth` logic where needed. Keep public preview data minimal.

- [ ] **Step 4: Fix membership principal routing**

Allow delete/update operations to use either `userId` or legacy `clientId` as the principal.

- [ ] **Step 5: Verify HTTP tests pass**

Run: `go test ./internal/web -run 'TestWorkspaceInvite' -count=1`

Expected: PASS.

### Task 4: Frontend Invite Flow

**Files:**
- Modify: `web/src/types.ts`
- Modify: `web/src/hooks/useDashboardData.ts`
- Modify: `web/src/views/WorkspacesView.tsx`
- Create: `web/src/views/InvitePage.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Add types and API actions**

Add invite types and data hook actions:

```ts
createWorkspaceInvite(workspaceId: string, role: WorkspaceRole): Promise<WorkspaceInviteCreated>
listWorkspaceInvites(workspaceId: string): Promise<WorkspaceInvite[]>
revokeWorkspaceInvite(workspaceId: string, inviteId: string): Promise<void>
previewInvite(token: string): Promise<InvitePreview>
registerWithInvite(token: string, username: string, password: string): Promise<PersonalPayload>
joinInvite(token: string): Promise<PersonalPayload>
```

- [ ] **Step 2: Update Workspaces page**

Replace the primary add-member client picker with invite generation/list/revoke/copy controls. Keep direct role edit and removal for existing members.

- [ ] **Step 3: Add invite page route**

Route `/invite/:token` in `App.tsx`. The page loads preview, handles register or join, then shows a device creation form and one-time token result.

- [ ] **Step 4: Build frontend**

Run: `cd web && npm run lint && npm run build`

Expected: PASS.

### Task 5: End-to-End Verification and Deploy

**Files:**
- Modify: embedded `web/dist` from build
- No additional code files unless verification exposes a bug.

- [ ] **Step 1: Run full local checks**

Run:

```bash
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
go build ./...
cd web && npm run lint && npm run build
```

Expected: all PASS.

- [ ] **Step 2: Local HTTP smoke**

Start local dashboard on an unused port and verify:

- admin creates invite;
- public preview works;
- invite registration creates user and workspace membership;
- new user creates device;
- device bearer `/api/me` returns client mode and workspace data.

- [ ] **Step 3: Commit and push**

Commit implementation with a terse message and push `cube20-init-43981` to origin.

- [ ] **Step 4: Build Linux binary and deploy devbox**

Build Linux amd64 binary, canary it on devbox, then restart `cube20`.

- [ ] **Step 5: Production smoke**

On devbox verify health/ready, invite creation/preview/register/device flow against test data, then revoke or disable any test principal created during smoke.
