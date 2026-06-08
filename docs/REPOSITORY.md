# Cube20 Repository Guide

This document is the maintenance map for the `cube20` repository.

## Purpose

`cube20` runs a cloud-managed Codex account pool. A central server owns
cloud-managed auth snapshots, chooses an account for each `cube run`, tracks
leases, refreshes quota, records usage, and hosts the dashboard. Local clients
keep only the cloud URL/PAT and their own Codex `config.toml`.

## Repository Layout

- `cmd/cube`: CLI entrypoint and cloud client commands.
- `internal/manager`: account state, leases, Postgres persistence, usage
  summaries, quota cache, client PATs, and load-balancer policy.
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
cube cloud config --server https://cube.example.com --token <cube_pat_...>
```

Then they run Codex through the pool:

```sh
cube run -- --model gpt-5
```

`cube run` claims an exclusive server lease, creates a temporary `CODEX_HOME`,
links the local Codex `config.toml`, runs Codex, uploads changed auth and usage,
releases the lease, then removes the temporary auth copy.

## Auth Ownership

Accounts have an owner mode:

- `cloud`: server owns the auth snapshot and can refresh quota server-side.
- `client`: local machine owns the auth and reports quota/usage via
  `cube report`; the load balancer does not lease it.

Cloud-owned accounts should be operated through `cube run` and
`cube cloud relogin`. Do not use bare `codex` against the same account while it
is in the cloud pool, because bare Codex is outside the server lease protocol.

## Load Balancer

The load balancer is quota-aware weighted round-robin. It excludes accounts that
are not ready, have missing auth, are leased, are client-owned, or have depleted
or invalid quota. Eligible accounts are scored with remaining 5h quota and reset
timing, so accounts near reset and accounts with more usable quota are preferred.

Lease lifecycle events are persisted and shown in the dashboard:

- `claimed`: an account was dispatched to a client/PAT.
- `released`: the client finished and released the lease.
- `expired`: the heartbeat expired and the server recovered the account.

The dashboard `Load Balancer` page shows:

- Routing map: in-pool/out-of-pool state, quota, score, reset, and recipient.
- 5h reset order: next quota reset sequence.
- Dispatch history: recent account-to-client dispatch events.

## Postgres Schema

The manager creates schema automatically and serializes schema initialization
with a transaction-level advisory lock.

Core tables:

- `cube_accounts`: account metadata, auth JSON, owner mode, generation, lease.
- `cube_clients`: PAT metadata and revocation state.
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
- `GET /api/dispatches`
- `GET|POST /api/clients`

Client PAT routes are restricted to lease, auth update, quota report, usage
report, and the personal dashboard:

- `POST /api/sync/leases`
- `PATCH /api/sync/leases/<lease-id>`
- `PUT /api/sync/leases/<lease-id>/auth`
- `DELETE /api/sync/leases/<lease-id>`
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

Example deploy files are in `deploy/`.

## Operational Rules

- Use one PAT per local operator or machine.
- Keep admin token for dashboard administration and cloud relogin only.
- Keep all cloud-managed accounts out of bare local Codex sessions.
- Stop `cube report` for accounts moved into the cloud-owned pool.
- Put cloud dashboard traffic behind HTTPS before uploading real auth JSON.
- Watch `Load Balancer -> Routing map` for pool eligibility.
- Watch `Load Balancer -> Dispatch history` to answer which account was sent to
  which client.
