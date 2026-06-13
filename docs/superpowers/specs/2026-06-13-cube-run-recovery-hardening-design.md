# cube run Recovery Hardening Design

## Goal

Harden the `cube run` execution path around account exhaustion and local bypasses without changing the current claim threshold policy. The system should make bare `codex` use visible, make quota state understandable, degrade safely when telemetry or resume is missing, recover expired leases predictably, and refresh stale quota before a new claim.

## Scope

In scope:

- Detect when the local live `~/.codex/auth.json` matches a cloud-managed account, so users can see when bare `codex` is bypassing leases.
- Improve quota state labels and reasons for ready-but-not-runnable accounts.
- Treat missing `rate_limits` telemetry as an explicit condition instead of silent success.
- Make swap/resume failures release the newly claimed lease and print a deterministic recovery hint.
- Keep TTL recovery, but make expired lease recovery status and errors readable.
- When a quota reset time has passed, refresh that account before deciding that a new `cube run` cannot use it.

Out of scope:

- Do not change the claim exclusion threshold. New `cube run` claims still exclude accounts at `<=5%` remaining. Runtime swap advice remains `<10%`.
- Do not implement process-level blocking of bare `codex`; this change reports and warns instead of deleting or mutating local credentials.
- Do not add queueing, fairness, per-user concurrency limits, or daemon-based zero-flicker resume.

## Design

### Bare codex detection

Add a local diagnostic that compares the identity derived from the live `auth.json` with the server-side managed accounts available to the current device token. The CLI should expose this as `cube cloud status` and print:

- registered device identity;
- workspace membership;
- whether the live `auth.json` matches a managed account;
- if it matches, a warning that direct `codex` bypasses leasing, heartbeat, return, and auto-swap.

The command must redact tokens and must never delete local auth.

### Quota state expression

Add a derived `RuntimeState` and `RuntimeReason` to load-balancer account views:

- `available`: can be selected now;
- `leased`: currently held by an active lease;
- `quota_cooldown`: reset time is in the future but remaining quota is below the claim threshold;
- `refresh_needed`: cached reset time has passed and a refresh is needed;
- `quota_telemetry_missing`: quota windows are missing or not checked;
- `unavailable`: status/auth/owner/quota status makes the account unavailable.

Existing eligibility rules remain authoritative. This is a display and API clarity layer.

### Missing runtime telemetry

Heartbeat currently records quota only when the client sends windows. If no `rate_limits` were parsed, heartbeat should still renew the lease, but the response should include `quotaTelemetryMissing: true`. The server should not overwrite an existing cache with empty data. The CLI should surface a short warning at most once per run when telemetry is missing.

### Swap and resume failure

Current swap flow claims a new lease before releasing the old one. If `newestSessionID`, `writeSnapshotToStableHome`, or resume startup fails after the new claim, the newly claimed lease must be released. If no session id exists, the old lease remains the active lease until final cleanup. If a new lease was claimed but cannot be used, release it and keep the old lease cleanup path honest.

The terminal should print a deterministic recovery hint, including the run directory and the manual `codex resume <session>` command when a session id is known.

### Expired lease recovery

TTL remains the fallback for `kill -9`, sleep, and network loss. When a lease expires, the account should move to `recovering` with a readable `LastError`, dispatch an `expired` event, and then `RecoverExpiredLeases` should refresh quota. Success returns it to `ready`; invalid refresh keeps it out of the pool.

### Claim-time refresh after reset

Before `ClaimLease` finalizes candidates, it should detect accounts whose cached 5h or 7d reset time has already passed and synchronously refresh those accounts outside the state lock. Then it reloads state and evaluates eligibility again. This avoids a confusing state where a reset has passed but `cube run` refuses to use the account until the background worker catches up.

## Testing

- Manager tests for runtime state labels.
- Manager tests that `ClaimLease` refreshes an account whose reset has passed before rejecting it.
- Web heartbeat tests for `quotaTelemetryMissing`.
- CLI lifecycle tests that swap failure releases a newly claimed lease.
- CLI tests for bare local auth detection using redacted summaries.

## Rollout

This is additive. Existing API fields and thresholds stay compatible. The only user-visible changes are clearer status strings, one-time telemetry warnings, safer swap cleanup, and the new `cube cloud status` diagnostic.
