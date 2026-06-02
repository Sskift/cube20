# cube20

`cube20` is a local Codex account-pool manager. Its binary command is `cube`.

The first milestone focuses on two local surfaces:

- A terminal TUI for account inventory and account-scoped Codex commands.
- A localhost dashboard for browser-based monitoring and control.

## Storage

`cube` keeps dashboard metadata outside the repository:

- State file: `~/.cube20/state.json`
- Account homes: `~/.codex-accounts/<account-id>`

Each account home is intended to hold its own Codex state, including its own
`auth.json` when file-based Codex credential storage is used. Treat every
account home like a secret-bearing directory.

## Planned Commands

```shell
cube
cube accounts list
cube accounts add work-plus
cube accounts login work-plus
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
