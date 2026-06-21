# cube20

`cube20` is a Codex account-pool manager. Its binary command is `cube`.
It runs a central, quota-aware Codex account pool with workspace isolation,
browser invite registration, per-device tokens, manual direct-Codex leases, and
a hosted dashboard for operations.

For repository structure, data model, deployment, and maintenance notes, see
[`docs/REPOSITORY.md`](docs/REPOSITORY.md).

The cloud deployment model is:

- One cloud `cube dashboard` server owns the account pool, auth snapshots,
  quota refreshes, usage stats, and the hosted dashboard.
- Local machines store only the cloud URL/device token, optional workspace
  preference, and their own Codex `config.toml`.
- Users join a workspace from an invite link, then mint a device token for the
  local `cube` CLI.
- Cloud-owned accounts are refreshed and leased by the server. Client-owned
  accounts are refreshed locally and reported back to the server.
- `cube run` asks the cloud server for an exclusive account lease, runs Codex
  with a temporary `CODEX_HOME`, heartbeats the lease, uploads refreshed auth
  and usage, releases the lease, then deletes the temporary local auth copy.
- Direct `codex` sessions can be protected with an explicit live lease via
  `cube cloud borrow-live`, `keepalive-live`, and `return-live`.

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
- `cube_users`
- `cube_sessions`
- `cube_workspaces`
- `cube_memberships`
- `cube_workspace_invites`
- `cube_usage`
- `cube_usage_events`
- `cube_dispatch_events`
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
cube help
cube version
cube doctor
cube dashboard
cube clients create macbook-a
cube clients list
cube clients revoke client-macbook-a
cube device config --server https://cube.example.com --token <cube_dev_...> --workspace <workspace-id>
cube cloud config --server https://cube.example.com --token <cube_pat_...> --workspace <workspace-id>
cube cloud status
cube cloud quota work-plus
cube cloud relogin work-plus
cube cloud borrow-live --account work-plus
cube cloud keepalive-live --watch --interval 60s
cube cloud return-live
cube run
cube run --heartbeat 20s -- --model gpt-5
cube report
cube report --daemon --interval 5m
cube config edit
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
- `deploy/devbox_deploy.sh`

The service exposes unauthenticated `GET /healthz` and `GET /readyz` probes.
`/readyz` verifies local state and Postgres connectivity when
`CUBE_DATABASE_URL` is set. In Postgres mode, lease selection uses a Postgres
advisory lock so multiple server processes do not hand out the same account.
Keep the service behind TLS because cloud-owned `auth.json` snapshots are stored
server-side.

For the current devbox test deployment, use the deploy helper:

```shell
deploy/devbox_deploy.sh
deploy/devbox_deploy.sh --smoke-only
```

By default it builds `GOOS=linux GOARCH=amd64`, uploads to
`liushiao@10.37.6.166:/data00/home/liushiao/cube20-deploy-test`, canaries on
port `8721`, restarts the dashboard on port `8720`, and smokes `/healthz` and
`/readyz`. The script sources the remote env file without printing token
values.

## Workspaces, Invites, And Devices

The intended onboarding flow is user-first:

1. A workspace admin creates a workspace in the dashboard or with
   `cube workspace create <name>`.
2. The workspace admin creates an invite link from the dashboard Workspaces
   page. The public link is `/invite/<token>`.
3. The invitee opens the link, registers a username/password, and joins that
   workspace.
4. The invitee mints a device token from the dashboard Devices page.
5. The local CLI is configured once:

```shell
cube device config --server https://cube.example.com --token <cube_dev_...> --workspace <workspace-id>
cube doctor
```

`cube cloud config` accepts the same server/token/device/workspace settings for
legacy PAT naming. New operator setup should prefer the user/device flow, not
manual server-side PAT sharing.

Workspace membership scopes lease claims, manual live leases, client reports,
quota reports, and personal dashboard reads. Platform-wide admin capabilities
remain limited to the cloud admin token; being a workspace admin does not grant
cross-workspace user/device/audit access.

## Cloud Clients And Device Tokens

Each local machine should authenticate with its own device token. A browser user
can mint device tokens after registering or joining a workspace. Legacy
admin-created PATs are still available for server-side setup and migration:

```shell
./bin/cube clients create macbook-a
./bin/cube clients create workstation-b
./bin/cube clients list
./bin/cube clients revoke client-macbook-a
```

Give each local operator only their generated device/PAT token. The token can
claim an exclusive account lease inside its workspace, heartbeat that lease,
upload refreshed auth, upload usage, upload client quota reports, perform manual
live borrow/return, and open the personal dashboard. It cannot pull arbitrary
auth snapshots, push arbitrary cloud-owned auth, change accounts, fetch
server-side cloud quota, or cross workspace boundaries. Use the admin token only
for dashboard administration, importing auth, cloud relogin, and platform-wide
user/device views.

## Cloud Run

```shell
# Save this once on each local machine.
./bin/cube device config --server https://cube.example.com --token <cube_dev_...> --workspace <workspace-id>
./bin/cube doctor

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

When quota is depleted or about to deplete, the next `cube run` claim excludes
that account and selects another eligible account in the same workspace. Lease
heartbeats can also return a swap hint so long-running sessions stop extending a
nearly depleted account and recover through the normal Codex resume path.

## Direct Codex Live Leases

Use the live-lease commands when Codex was started directly, for example
`codex` from a shell or a Codex session started outside `cube run`. This marks
the current live `~/.codex/auth.json` as actively rented without refreshing or
replacing it.

```shell
# Upload the current live auth identity and lease the matching account.
cube cloud borrow-live --account 137 --ttl 8h

# Keep that manual lease alive while the direct Codex session is open.
cube cloud keepalive-live --watch --interval 60s

# Release the matching manual lease after the direct Codex session is done.
cube cloud return-live
```

`borrow-live` verifies that the live auth matches the requested managed account
before leasing it. `keepalive-live` preserves the longer manual TTL instead of
shrinking it to the short `cube run` heartbeat TTL, and reports usage from the
live Codex home. `return-live` can identify the current live auth automatically;
use `--account` or `--lease` when returning from a machine that no longer has
that live auth.

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
the cloud server. If an account is moved into the cloud-owned pool, stop
`cube report` for that local auth; the server refreshes and displays its quota
from the cloud copy:

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
the same time unless the bare session has an active manual live lease. Bare
`codex` only knows the local `~/.codex/auth.json`; `cube run` leases are created
inside temporary Codex homes and cannot see an already-running direct Codex
process.

For cloud-owned accounts, dashboard refresh and `cube cloud quota <id>` are
server-side refreshes. For client-owned accounts, those same cloud reads return
the latest client-reported cache instead of refreshing the server copy.

## Cloud API

The cloud sync API is for `cube run`, `cube report`, cloud relogin, live
leases, browser onboarding, and the hosted dashboard. The old local-pool
`cube sync` CLI is no longer exposed.

The main cloud endpoints are:

- `POST /api/auth/register`
- `POST /api/auth/login`
- `POST /api/auth/logout`
- `GET /api/auth/me`
- `GET|POST /api/devices`
- `DELETE /api/devices/<device-id>`
- `GET /api/invites/<token>`
- `POST /api/invites/<token>/register`
- `POST /api/invites/<token>/join`
- `GET|POST /api/workspaces`
- `GET|POST /api/workspaces/<workspace-id>/members`
- `DELETE /api/workspaces/<workspace-id>/members/<principal-id>`
- `GET|POST /api/workspaces/<workspace-id>/invites`
- `DELETE /api/workspaces/<workspace-id>/invites/<invite-id>`
- `POST /api/sync/push`
- `POST /api/sync/identify-auth`
- `POST /api/sync/claim` (legacy lease claim response)
- `POST /api/sync/leases`
- `PATCH /api/sync/leases/<lease-id>`
- `PUT /api/sync/leases/<lease-id>/auth`
- `DELETE /api/sync/leases/<lease-id>`
- `POST /api/sync/manual-borrow`
- `POST /api/sync/manual-return`
- `POST /api/sync/usage`
- `GET /api/sync/quota/<id>`
- `POST /api/sync/quota/<id>` (client quota report)
- `GET /api/me`
- `GET /api/users`
- `PATCH /api/users/<user-id>/status`
- `GET /api/stats`
- `GET /api/refresh-queue`
- `GET /api/dispatches`
- `GET|POST /api/clients`
- `GET /healthz`
- `GET /readyz`

Admin routes require `Authorization: Bearer <admin-token>` when
`CUBE_CLOUD_TOKEN` or `--cloud-token` is configured. Sync routes accept either
the admin token or a device/client PAT, but PATs are intentionally restricted to
lease, manual live lease, report, and personal-dashboard operations. PATs cannot
pull auth snapshots, push unleased cloud-owned auth, or manage workspace
membership. Browser routes use an HttpOnly `cube_session` cookie.

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
usage, dispatch history, and the 5h quota refresh queue.

Postgres deployments also keep per-run/per-model rows in `cube_usage_events`.
The current dashboard still reads the compact `cube_usage` summary; the event
table is the durable source for future per-session views.

`cube_dispatch_events` records load-balancer lease lifecycle events:
`claimed`, `released`, and `expired`. The dashboard uses this table to show
which account was sent to which client/PAT holder.
