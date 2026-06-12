# Workspace Invite Registration Design

## Goal

Make workspace onboarding work through invite links:

1. A workspace admin creates an invite link from a workspace.
2. The invited person opens the link.
3. If they are new, they register a username and password through the invite page.
4. The server adds that user to the workspace.
5. The user creates their own device token after joining.

This replaces the current immature "pick a client/device" member flow as the primary path. Device tokens remain user-owned implementation details, not the thing admins invite.

## Current Gaps

- The backend can store workspace membership by `userId`, but the UI still invites by `clientId`.
- A workspace admin cannot generate a self-serve join path for a new user.
- The member list mostly shows ids, not user-oriented identity.
- Removing or updating members still assumes `clientId` in the URL.
- New users have no guided path from registration into device-token creation.

## Recommended Approach

Add server-side workspace invite records and a public invite registration page. The first version should use reusable links with an expiry and revocation, because this is useful for a small internal team and avoids repeatedly creating one-off links.

Defaults:

- Role: `member`.
- Expiry: 7 days.
- Max uses: unlimited for the first version.
- Token storage: only a hash is persisted.
- Link disclosure: plaintext invite token is returned only when an invite is created.

## Data Model

Add `WorkspaceInvite`:

- `ID`: stable invite id.
- `WorkspaceID`: target workspace.
- `Role`: `member` or `admin`; UI defaults to `member`.
- `TokenHash`: hash of the secret token.
- `CreatedBy`: user id or `admin`.
- `CreatedAt`, `ExpiresAt`.
- `RevokedAt`: optional.
- `UsedCount`: number of successful joins.
- `LastUsedAt`: optional.

File state stores `Invites []WorkspaceInvite`. Postgres adds `cube_workspace_invites`.

The plaintext invite token is generated as a high-entropy random value with a `cube_inv_` prefix. It is not stored after creation.

## Backend API

Workspace-admin authenticated routes:

- `POST /api/workspaces/{workspaceId}/invites`
  - Body: optional `{ "role": "member", "expiresInHours": 168 }`.
  - Returns: invite metadata plus `url` and plaintext `token`.

- `GET /api/workspaces/{workspaceId}/invites`
  - Lists invite metadata without plaintext tokens.

- `DELETE /api/workspaces/{workspaceId}/invites/{inviteId}`
  - Revokes an invite.

Public routes:

- `GET /api/invites/{token}`
  - Returns safe invite preview: workspace name, role, expiry, validity.
  - Does not reveal members, accounts, or admin-only data.

- `POST /api/invites/{token}/register`
  - Body: `{ "username": "...", "password": "..." }`.
  - Creates a user session.
  - Adds the new user to the workspace with the invite role.
  - Increments invite usage.
  - Returns the normal user payload plus workspace membership.

Authenticated route:

- `POST /api/invites/{token}/join`
  - For an already logged-in user.
  - Adds the current user to the workspace without creating a new account.

Existing membership endpoints should accept and return user principals cleanly:

- `POST /api/workspaces/{workspaceId}/members` continues to accept `userId` for direct admin edits.
- `DELETE /api/workspaces/{workspaceId}/members/{principalId}` removes either a user id or legacy client id.
- Member listing should include username when membership is user-backed.

## Frontend UX

Workspaces page:

- Replace the primary "select client" add-member UI with an "Invite link" control.
- Show active invites for the selected workspace with expiry, role, use count, revoke button, and copy link action.
- Keep direct member role editing/removal, but label users by username where possible.
- Legacy client-only members remain visible as device/client rows.

Invite page:

- Route: `/invite/:token`.
- Load invite preview.
- If not logged in, show username/password registration.
- If logged in, show "Join workspace" using the current account.
- After successful registration/join, show a device creation panel.
- Device token is displayed once, with copy support and clear warning text.

Personal dashboard:

- Keep device management available after the invite flow.
- Ensure `/api/me` returns enough workspace and device data for the post-join state.

## Error Handling

- Expired, revoked, malformed, or unknown invite tokens return a clear invalid-invite state.
- Duplicate usernames return the existing username error and keep the user on the invite page.
- If a logged-in user already belongs to the workspace, joining is idempotent and returns success.
- Disabled users cannot join via invite.
- Workspace admins cannot revoke the last admin membership through this flow.

## Security

- Store only token hashes.
- Limit invite creation to workspace admins or platform admins.
- Public invite preview exposes only workspace name, role, and validity.
- Joining consumes server-side validation at the moment of registration/join, not only preview validation.
- Registration and join should reuse the login/register rate limiter where practical.

## Testing

Backend tests:

- Workspace admin can create an invite and list it without plaintext token.
- Non-admin cannot create/list/revoke workspace invites.
- Public preview works for valid invites and rejects expired/revoked/unknown tokens.
- Registering through an invite creates a user session and workspace membership.
- Logged-in join adds the user id and is idempotent.
- Revoke makes the invite unusable.
- Device token created after invite registration authenticates and sees joined workspace through `/api/me`.

Frontend/API integration tests:

- Workspaces page can create/copy/revoke an invite.
- Invite page shows invalid/expired states.
- Invite registration lands in user mode and can create a device.
- Member list displays username-backed memberships and supports role updates/removal.

## Deployment Notes

Postgres migration must be additive. Existing workspaces, memberships, users, and devices continue to work. No existing client token format changes.
