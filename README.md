# cube20

`cube20` is a local Codex profile-pool manager. Its binary command is `cube`.

The first milestone focuses on two local surfaces:

- A terminal TUI for account inventory and account-scoped Codex commands.
- A localhost dashboard for browser-based monitoring and control.

## Storage

`cube` keeps dashboard metadata outside the repository:

- State file: `~/.cube20/state.json`
- Account homes: `~/.codex-accounts/<account-id>`

Each account home is intended to hold a Codex profile snapshot:

- `auth.json`
- `config.toml`

Treat every account home like a secret-bearing directory.

## Planned Commands

```shell
cube
cube accounts list
cube accounts add work-plus
cube accounts import work-plus
cube accounts login work-plus
cube accounts quota work-plus
cube accounts usage work-plus
cube profile deploy work-plus
cube dashboard
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
