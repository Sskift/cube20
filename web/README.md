# cube20 dashboard

HeroUI + React dashboard embedded by the `cube` binary. It is the operator UI
for accounts, load balancing, quota, workspaces, invite links, user sessions,
devices, manual direct-Codex leases, and dispatch history.

```sh
npm install
npm run build
```

The Go server embeds `web/dist`, so rebuild the dashboard before building the `cube` binary when frontend files change.

Useful local checks:

```sh
npm run lint
npm run build
cd ..
go test ./internal/web ./cmd/cube
```

Authentication surfaces:

- Platform admins use the cloud admin token.
- Browser users register/login with a session cookie.
- Local machines use device/PAT tokens minted from the dashboard.
- Invite links are public only for preview/register/join; workspace management
  still requires a workspace admin session.
