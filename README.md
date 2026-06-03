# cube20

`cube20` is a local Codex profile-pool manager. Its binary command is `cube`.

The first milestone focuses on two local surfaces:

- A terminal TUI for account inventory and account-scoped Codex commands.
- A localhost dashboard for browser-based monitoring and control.

## Storage

`cube` keeps dashboard metadata outside the repository:

- State file: `~/.cube20/state.json`
- Settings file: `~/.cube20/settings.toml`
- Account homes: `~/.codex-accounts/<account-id>`

Each account home is intended to hold a Codex profile snapshot:

- `auth.json`
- `config.toml`

`settings.toml` defaults to the official Codex home rules: `$CODEX_HOME` when
set, otherwise `~/.codex`. It also records the managed account directory so the
dashboard can discover existing managed profiles. Cloud clients can also store
`cloud_url` and `cloud_token` there so sync commands do not need environment
variables on every run.

Treat every account home like a secret-bearing directory.

## Commands

```shell
cube
cube accounts list
cube accounts add work-plus
cube accounts import
cube accounts login work-plus
cube accounts quota work-plus
cube accounts usage work-plus
cube profile deploy work-plus
cube dashboard
cube cloud config --server https://cube.example.com --token <token>
cube cloud-run
cube sync push work-plus
cube sync pull work-plus --deploy
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

For a central dashboard, bind it to the network and require a shared bearer
token:

```shell
CUBE_CLOUD_TOKEN="$(openssl rand -hex 32)" ./bin/cube dashboard --host 0.0.0.0 --port 8720
```

You can also persist the token in `~/.cube20/settings.toml` on the server:

```shell
./bin/cube cloud config --token <token>
./bin/cube dashboard --host 0.0.0.0 --port 8720
```

Open the hosted dashboard with `?token=<token>` once; the browser stores that
token locally and sends it on future API requests. Put the service behind HTTPS
before sending real `auth.json` data over the network.

## Cloud Sync

Cloud sync turns one `cube dashboard` process into the central account pool.
Local machines can push refreshed auth snapshots to it, pull a named account
back down, or claim the next load-balanced account.

```shell
# Save this once on each local machine.
./bin/cube cloud config --server https://cube.example.com --token <same token as the dashboard>

# Ask the cloud load balancer for the next ready account, write that auth
# snapshot locally, run Codex in the current directory, then push refreshed auth
# back to the cloud when Codex exits.
cd ~/work/a
./bin/cube cloud-run

cd ~/work/b
./bin/cube cloud-run -- --model gpt-5

# Send one local managed auth snapshot to the cloud.
./bin/cube sync push work-plus

# Keep every local managed auth snapshot flowing to the cloud; --pull also
# accepts newer cloud copies of the same account.
./bin/cube sync daemon --all --pull --interval 60s

# Pull a specific cloud account into the local pool and activate it.
./bin/cube sync pull work-plus --deploy
```

Environment variables still work and override the saved settings:

```shell
export CUBE_CLOUD_URL=https://cube.example.com
export CUBE_CLOUD_TOKEN=<token>
```

The cloud endpoints are:

- `POST /api/sync/push`
- `GET /api/sync/pull/<id>`
- `POST /api/sync/claim`

All `/api/*` routes require `Authorization: Bearer <token>` when
`CUBE_CLOUD_TOKEN` or `--cloud-token` is configured.

## Quota

`cube accounts quota <id>` reads the account profile's `auth.json`. For ChatGPT
OAuth logins it calls the hard-coded ChatGPT usage endpoint and normalizes the
5h, 7d, and code-review windows. API-key-only Codex profiles are reported as
unsupported because ChatGPT subscription quota is not available from an API key.

## Local Usage

`cube accounts usage <id>` scans the profile's local Codex session JSONL files
under `sessions/` and `archived_sessions/`. It reports today, seven-day, and
all-time token totals. This is an observation aid only; account selection should
still prioritize subscription quota from `cube accounts quota`.
