# Cube20 Repository Guide

This document is the maintenance map for the `cube20` repository.

## Purpose

`cube20` runs a cloud-managed Codex account pool. A central server owns
cloud-managed auth snapshots, chooses an account for each `cube run`, tracks
leases, refreshes quota, records usage, and hosts the dashboard. Workspace users
join through invite links, mint per-device tokens, and keep only the cloud URL,
device token, optional default workspace, and their own Codex `config.toml` on
local machines.

## Repository Layout

- `cmd/cube`: CLI entrypoint and cloud client commands.
- `internal/manager`: account state, leases, Postgres persistence, users,
  sessions, workspaces, invites, device/PAT tokens, usage summaries, quota
  cache, and load-balancer policy.
- `internal/quota`: Codex/ChatGPT quota probing and normalization.
- `internal/usage`: local Codex JSONL usage scanning.
- `internal/web`: HTTP API, dashboard server, health probes, quota worker.
- `web`: React + HeroUI dashboard source and built `web/dist` assets.
- `deploy`: example systemd, nginx, and environment files.
- `docs`: repository and operations documentation.

## Runtime Model

The server starts with:

```sh
cube dashboard --host 0.0.0.0 --port 8720
```

Important server settings:

- `CUBE_DATABASE_URL`: Postgres DSN. Required for production-grade shared state.
- `CUBE_CLOUD_TOKEN`: admin bearer token for dashboard administration.
- `CUBE_QUOTA_REFRESH_INTERVAL`: optional server-side quota worker interval.

Local clients configure access once:

```sh
cube device config --server https://cube.example.com --token <cube_dev_...> --workspace <workspace-id>
cube doctor
```

Then they run Codex through the pool:

```sh
cube run -- --model gpt-5
```

`cube run` claims an exclusive server lease, creates a temporary `CODEX_HOME`,
links the local Codex `config.toml`, runs Codex, uploads changed auth and usage,
releases the lease, then removes the temporary auth copy.

The browser onboarding flow is user-first:

1. A workspace admin creates a workspace.
2. The workspace admin creates a `/invite/<token>` link.
3. The invitee registers a username/password from that link.
4. The invitee creates a device token from the dashboard.
5. The local `cube` CLI uses that token for `cube run`, `cube report`, manual
   live leases, and personal dashboard reads.

## Auth Ownership

Accounts have an owner mode:

- `cloud`: server owns the auth snapshot and can refresh quota server-side.
- `client`: local machine owns the auth and reports quota/usage via
  `cube report`; the load balancer does not lease it.

Cloud-owned accounts should be operated through `cube run` and
`cube cloud relogin`. Do not use bare `codex` against the same account while it
is in the cloud pool unless that direct session has an active manual live lease:

```sh
cube cloud borrow-live --account <id-or-label> --ttl 8h
cube cloud keepalive-live --watch --interval 60s
cube cloud return-live
```

Manual live leases identify the current live `~/.codex/auth.json`, verify it
matches the selected managed account, and keep the server lease table aligned
with direct Codex use outside `cube run`.

## Load Balancer

The load balancer is workspace-scoped, quota-aware weighted round-robin. It
excludes accounts that are not ready, have missing auth, are leased, are
client-owned, belong to another workspace, or have depleted or invalid quota.
Eligible accounts are scored with remaining 5h quota and reset timing, so
accounts near reset and accounts with more usable quota are preferred.

Lease lifecycle events are persisted and shown in the dashboard:

- `claimed`: an account was dispatched to a client/PAT.
- `released`: the client finished and released the lease.
- `expired`: the heartbeat expired and the server recovered the account.
- `manual` lease holders are direct Codex sessions protected with
  `borrow-live`/`keepalive-live`/`return-live`.

The dashboard `Load Balancer` page shows:

- Routing map: in-pool/out-of-pool state, quota, score, reset, and recipient.
- 5h reset order: next quota reset sequence.
- Reset-credit count: remaining Codex manual reset credits, with an admin
  action in account details to consume one server-side reset credit.
- Dispatch history: recent account-to-client dispatch events.

Heartbeat responses can include a swap hint when an active account is near
depletion. `cube run` then stops extending that lease and lets the normal Codex
resume flow continue on a fresher account.

## Postgres Schema

The manager creates schema automatically and serializes schema initialization
with a transaction-level advisory lock.

Core tables:

- `cube_accounts`: account metadata, auth JSON, owner mode, generation, lease.
- `cube_clients`: PAT metadata and revocation state.
- `cube_users`: browser users.
- `cube_sessions`: hashed browser session cookies.
- `cube_workspaces`: workspace metadata.
- `cube_memberships`: workspace roles for users and legacy clients.
- `cube_workspace_invites`: hashed invite tokens, role, expiry, revocation, and
  use counters.
- `cube_usage`: compact per-account usage summary.
- `cube_usage_events`: durable per-run/per-model usage events.
- `cube_dispatch_events`: load-balancer lease lifecycle history.
- `cube_quota_cache`: latest quota result and quota source.
- `cube_meta`: small server metadata such as round-robin cursor.

For production, use Postgres mode. File-state mode is useful for local
development, but it is not intended for multiple cloud server instances.

## Dashboard API

Admin routes require the admin token:

- `GET /api/accounts`
- `POST /api/accounts/import-json`
- `GET /api/lb/status`
- `GET /api/refresh-queue`
- `GET|POST /api/clients`
- `GET /api/users`
- `PATCH /api/users/{id}/status`

Browser/session routes:

- `POST /api/auth/register`
- `POST /api/auth/login`
- `POST /api/auth/logout`
- `GET /api/auth/me`
- `GET|POST /api/devices`
- `DELETE /api/devices/{id}`
- `GET|POST /api/workspaces`
- `GET|POST /api/workspaces/{id}/members`
- `GET|POST /api/workspaces/{id}/invites`
- `DELETE /api/workspaces/{id}/invites/{inviteId}`
- `GET /api/dispatches` (session-scoped unless called with admin token)

Public invite routes:

- `GET /api/invites/{token}`
- `POST /api/invites/{token}/register`
- `POST /api/invites/{token}/join`

Device/PAT routes are restricted to lease, manual live lease, auth update,
quota report, usage report, and the personal dashboard. Workspace management is
session/admin scoped:

- `POST /api/sync/leases`
- `PATCH /api/sync/leases/<lease-id>`
- `PUT /api/sync/leases/<lease-id>/auth`
- `DELETE /api/sync/leases/<lease-id>`
- `POST /api/sync/identify-auth`
- `POST /api/sync/manual-borrow`
- `POST /api/sync/manual-return`
- `POST /api/sync/usage`
- `POST /api/sync/quota/<account-id>`
- `GET /api/me`

Health probes:

- `GET /healthz`: process is serving.
- `GET /readyz`: local state and Postgres connectivity are ready.

## Build And Test

Run Go tests:

```sh
go test ./...
```

Build the dashboard:

```sh
cd web
npm run build
```

Build the embedded binary after dashboard changes:

```sh
go build -o bin/cube ./cmd/cube
```

The Go server embeds `web/dist`, so frontend changes must be built before the
final Go binary is built or deployed.

## Deployment Checklist

1. Sync repository files to the server.
2. Run `go test ./...` on the server.
3. Run `npm run build` in `web`.
4. Run `go build -o bin/cube ./cmd/cube`.
5. Restart the systemd service.
6. Check `curl http://127.0.0.1:8720/readyz`.

Example deploy files are in `deploy/`. For the current devbox deployment, the
helper wraps the build, canary, restart, and smoke test:

```sh
deploy/devbox_deploy.sh
deploy/devbox_deploy.sh --smoke-only
```

The default devbox target is
`liushiao@10.37.6.166:/data00/home/liushiao/cube20-deploy-test`, production
port `8720`, and canary port `8721`.

## Operational Rules

- Use one PAT per local operator or machine.
- Prefer browser-created device tokens for new users; use admin-created PATs
  only for setup, migration, or compatibility.
- Keep admin token for platform administration, imports, and cloud relogin only.
- Keep all cloud-managed accounts out of bare local Codex sessions unless a
  manual live lease is active and heartbeating.
- Stop `cube report` for accounts moved into the cloud-owned pool.
- Put cloud dashboard traffic behind HTTPS before uploading real auth JSON.
- Watch `Load Balancer -> Routing map` for pool eligibility.
- Watch `Load Balancer -> Dispatch history` to answer which account was sent to
  which client.
- Use `cube doctor` on local machines before debugging a lease issue; it reports
  server/token/device/workspace configuration and whether the live auth matches
  a managed account.
