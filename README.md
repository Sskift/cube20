# cube20

`cube20` is a Codex account-pool manager. Its binary command is `cube`.

The cloud deployment model is:

- One cloud `cube dashboard` server owns the account pool, auth snapshots,
  quota refreshes, usage stats, and the hosted dashboard.
- Local machines store only the cloud URL/PAT and their own Codex
  `config.toml`.
- Cloud-owned accounts are refreshed and leased by the server. Client-owned
  accounts are refreshed locally and reported back to the server.
- `cube run` asks the cloud server for an exclusive account lease, runs Codex
  with a temporary `CODEX_HOME`, heartbeats the lease, uploads refreshed auth
  and usage, releases the lease, then deletes the temporary local auth copy.

## Storage

Local `cube` keeps only client/runtime metadata outside the repository:

- State file: `~/.cube20/state.json`
- Settings file: `~/.cube20/settings.toml`
- Optional legacy account homes: `~/.codex-accounts/<account-id>`

Cloud servers should set `CUBE_DATABASE_URL` or `database_url` in
`settings.toml`. When configured, account auth, owner mode, lease
state/generation, client PATs, usage stats, quota cache/source, and
load-balancer cursor are persisted in Postgres.

The managed Postgres tables are created automatically:

- `cube_accounts`
- `cube_clients`
- `cube_usage`
- `cube_usage_events`
- `cube_quota_cache`
- `cube_meta`

`settings.toml` defaults to the official Codex home rules: `$CODEX_HOME` when
set, otherwise `~/.codex`. Cloud clients can store `cloud_url` and
`cloud_token` there so `cube run` and `cube cloud quota` do not need
environment variables on every run.

Codex `config.toml` is local-machine state. `cube run` does not download or
overwrite it; it links the temporary Codex home back to the local
`$CODEX_HOME/config.toml` or `~/.codex/config.toml`. Use `cube config edit` to
open that local Codex config file.

## Commands

```shell
cube
cube dashboard
cube clients create macbook-a
cube clients list
cube clients revoke client-macbook-a
cube cloud config --server https://cube.example.com --token <cube_pat_...>
cube cloud quota work-plus
cube cloud relogin work-plus
cube run
cube run --heartbeat 20s -- --model gpt-5
cube report
cube report --daemon --interval 5m
cube config edit
cube sync push work-plus
cube sync daemon --all --pull --interval 60s
```

## Build

```shell
go build -o bin/cube ./cmd/cube
```

## Dashboard

```shell
./bin/cube dashboard
```

The dashboard listens on `http://127.0.0.1:8720` by default.

For a central dashboard, bind it to the network and require an admin bearer
token:

```shell
export CUBE_DATABASE_URL=postgres://cube:secret@db.example.com:5432/cube?sslmode=require
export CUBE_CLOUD_TOKEN="$(openssl rand -hex 32)"
export CUBE_QUOTA_REFRESH_INTERVAL=5m
./bin/cube dashboard --host 0.0.0.0 --port 8720
```

You can also persist the token in `~/.cube20/settings.toml` on the server:

```shell
./bin/cube cloud config --token <token>
./bin/cube dashboard --host 0.0.0.0 --port 8720
```

Open the hosted dashboard with `?token=<token>` once; the browser stores that
token locally and sends it on future API requests. Put the service behind HTTPS
before sending real `auth.json` data over the network.

Production examples live under `deploy/`:

- `deploy/cube-server.env.example`
- `deploy/cube20.service`
- `deploy/nginx-cube20.conf`

The service exposes unauthenticated `GET /healthz` and `GET /readyz` probes.
`/readyz` verifies local state and Postgres connectivity when
`CUBE_DATABASE_URL` is set. In Postgres mode, lease selection uses a Postgres
advisory lock so multiple server processes do not hand out the same account.
Keep the service behind TLS because cloud-owned `auth.json` snapshots are stored
server-side.

## Cloud Clients

Each local machine should authenticate with its own PAT. Create PATs on the
cloud server:

```shell
./bin/cube clients create macbook-a
./bin/cube clients create workstation-b
./bin/cube clients list
./bin/cube clients revoke client-macbook-a
```

Give each local operator only their generated `cube_pat_...` token. The PAT can
claim an exclusive account lease, heartbeat that lease, upload refreshed auth,
upload usage, upload client quota reports, and open the personal dashboard. It
cannot pull arbitrary auth snapshots, push arbitrary cloud-owned auth, change
accounts, or fetch server-side cloud quota. Use the admin token only for
dashboard administration, importing auth, and cloud relogin.

## Cloud Run

```shell
# Save this once on each local machine.
./bin/cube cloud config --server https://cube.example.com --token <cube_pat_...>

# Ask the cloud load balancer for the next ready, unleased account. cube keeps
# the lease alive while Codex runs, uploads auth.json changes, uploads token
# usage, then releases the lease.
cd ~/work/a
./bin/cube run

cd ~/work/b
./bin/cube run -- --model gpt-5

# Optional: tune the heartbeat interval. The server grants a longer TTL than the
# heartbeat interval, so normal long-running sessions stay leased.
./bin/cube run --heartbeat 20s

# Ask the server to refresh and return quota. This does not read local auth.
./bin/cube cloud quota work-plus

# Codex config stays local. This opens $CODEX_HOME/config.toml, or
# ~/.codex/config.toml when CODEX_HOME is not set.
./bin/cube config edit
```

Environment variables still work and override the saved settings:

```shell
export CUBE_CLOUD_URL=https://cube.example.com
export CUBE_CLOUD_TOKEN=<cube_pat_...>
```

Cloud run uses exclusive leases. A single cloud-owned account is never handed to
two active `cube run` sessions at the same time. Each lease records the client,
heartbeat time, expiry time, and auth generation. If Codex rotates auth while
running, cube uploads the new `auth.json` with the current generation. Stale or
late uploads are rejected instead of overwriting a newer server copy.

If the local process or network is interrupted long enough for the heartbeat to
expire, the server clears the lease and moves that account to `recovering`.
During recovery it verifies the last uploaded auth with a quota refresh; success
returns the account to `ready`, while invalidated refresh tokens stay out of the
pool until a fresh login/auth upload.

## Cloud Relogin

Use relogin when a cloud-owned account shows `refresh_token_invalidated` or is
kept in `drain` after a failed refresh:

```shell
./bin/cube cloud config --server https://cube.example.com --token <admin-token>
./bin/cube cloud relogin skift --status ready --owner cloud
```

`cube cloud relogin` creates a temporary `CODEX_HOME`, runs
`codex login --device-auth`, uploads the resulting `auth.json` to the cloud,
checks quota once, and deletes the temporary local auth copy. It does not modify
the operator's `~/.codex/auth.json`. This command replaces stored cloud auth and
therefore requires the admin token, not a client PAT.

## Local Reports

Use `cube report` for accounts that should stay owned by the local Codex
profile, such as a personal `~/.codex/auth.json` that must not be refreshed by
the cloud server:

```shell
# One-shot report of local auth, usage, and quota.
./bin/cube report

# Keep local auth/usage/quota flowing to the cloud dashboard.
./bin/cube report --daemon --interval 5m
```

`cube report` marks the uploaded account as `client` owned. The local machine
refreshes quota with its own `auth.json`, uploads the refreshed auth snapshot,
uploads usage stats, and posts the quota result to the server cache. The cloud
dashboard shows that quota as `client report`, and the load balancer will not
lease that account to `cube run`.

Do not use bare `codex` and `cube run` to share the same cloud-owned account at
the same time. Bare `codex` only knows the local `~/.codex/auth.json`; cloud
leases only protect sessions started through `cube run`.

For cloud-owned accounts, dashboard refresh and `cube cloud quota <id>` are
server-side refreshes. For client-owned accounts, those same cloud reads return
the latest client-reported cache instead of refreshing the server copy.

## Cloud Sync Admin Tools

These commands are migration/debug tools for moving existing local auth
snapshots into the cloud pool. Normal local usage should prefer `cube run`.

```shell
# Send one local managed auth snapshot to the cloud.
./bin/cube sync push work-plus

# Keep every local managed auth snapshot flowing to the cloud; --pull also
# accepts newer cloud copies of the same account.
./bin/cube sync daemon --all --pull --interval 60s

# Pull a specific cloud account into the local pool and activate it.
./bin/cube sync pull work-plus --deploy
```

The cloud endpoints are:

- `POST /api/sync/push`
- `GET /api/sync/pull/<id>`
- `POST /api/sync/claim` (legacy lease claim response)
- `POST /api/sync/leases`
- `PATCH /api/sync/leases/<lease-id>`
- `PUT /api/sync/leases/<lease-id>/auth`
- `DELETE /api/sync/leases/<lease-id>`
- `POST /api/sync/usage`
- `GET /api/sync/quota/<id>`
- `POST /api/sync/quota/<id>` (client quota report)
- `GET /api/stats`
- `GET /api/refresh-queue`
- `GET|POST /api/clients`
- `GET /healthz`
- `GET /readyz`

Admin routes require `Authorization: Bearer <admin-token>` when
`CUBE_CLOUD_TOKEN` or `--cloud-token` is configured. Sync routes accept either
the admin token or a client PAT, but PATs are intentionally restricted to lease
and report operations. PATs cannot pull auth snapshots or push unleased
cloud-owned auth.

## Quota

For cloud-owned accounts, use `cube cloud quota <id>`. It asks the cloud server
to refresh quota using the server-owned auth snapshot and returns the result.

For client-owned accounts, use `cube report` or `cube report --daemon`; the
local machine refreshes quota with its own auth, then posts that result to the
cloud cache. Cloud reads return the latest client-reported quota and do not
refresh the server copy.

For ChatGPT OAuth logins the quota fetcher calls the ChatGPT usage endpoint and
normalizes the 5h, 7d, and code-review windows. API-key-only Codex profiles are
reported as unsupported because ChatGPT subscription quota is not available
from an API key.

## Usage Stats

`cube run` summarizes the temporary Codex session JSONL files after Codex exits
and uploads today, seven-day, all-time, and per-model token totals to the cloud.
The dashboard shows the cleaned account view, connected clients, per-account
usage, and the 5h quota refresh queue.

Postgres deployments also keep per-run/per-model rows in `cube_usage_events`.
The current dashboard still reads the compact `cube_usage` summary; the event
table is the durable source for future per-session views.
