// Shared dashboard types. These mirror the Go JSON tags exactly — this is the
// existing /api/* contract and must not drift from the backend.

export type AccountStatus = "ready" | "recovering" | "drain" | "disabled";
export type AccountOwnerMode = "cloud" | "client";

// HeroUI semantic colour names accepted by Chip / ProgressBar / Avatar.
export type ChipColor = "default" | "accent" | "success" | "warning" | "danger";

export type TranslateFn = (zh: string, en: string) => string;

export interface Account {
  id: string;
  label: string;
  plan: string;
  status: AccountStatus;
  codexHome: string;
  workspaceId?: string;
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  generation?: number;
  leaseId?: string;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
  authPresent: boolean;
  authPath: string;
  configPresent: boolean;
  configPath: string;
  active: boolean;
  leaseActive?: boolean;
}

export interface Meta {
  statePath: string;
  settingsPath: string;
  accountsDir: string;
  liveCodexHome: string;
  liveAuthPresent: boolean;
  liveConfigPresent: boolean;
}

export interface QuotaItem {
  label: string;
  usedPercent: number;
  usedDisplay: string;
  remainingDisplay: string;
  resetsAt?: string;
}

export interface QuotaResult {
  status: string;
  plan?: string;
  source?: string;
  detail?: string;
  quotas?: QuotaItem[];
}

export interface UsageToken {
  total: number;
  input: number;
  cachedInput: number;
  output: number;
}

export interface ModelUsage {
  model: string;
  today: UsageToken;
  sevenDays: UsageToken;
  allTime: UsageToken;
  latestAt?: string;
}

export interface AccountUsage {
  accountId: string;
  clientId?: string;
  updatedAt: string;
  latestAt?: string;
  latestModel?: string;
  today: UsageToken;
  sevenDays: UsageToken;
  allTime: UsageToken;
  models?: ModelUsage[];
}

export interface DispatchEvent {
  id: string;
  leaseId: string;
  accountId: string;
  accountLabel?: string;
  clientId?: string;
  clientLabel?: string;
  holder?: string;
  event: string;
  generation?: number;
  createdAt: string;
  startedAt?: string;
  expiresAt?: string;
  // Website-user / device attribution (added with the user/device feature).
  userId?: string;
  username?: string;
  deviceId?: string;
  deviceLabel?: string;
}

// A website user account (USERNAME + PASSWORD, no email). `User` is the minimal
// identity returned by login/register/me; `UserView` is the admin roster row.
export interface User {
  id: string;
  username: string;
  createdAt?: string;
  lastLoginAt?: string;
  disabled?: boolean;
  deviceCount?: number;
}

export interface UserView {
  id: string;
  username: string;
  createdAt: string;
  lastLoginAt?: string;
  disabled: boolean;
  deviceCount: number;
}

// A per-user device token. The raw token is only ever returned once, on create
// (see DeviceCreated); thereafter only metadata is exposed.
export interface Device {
  id: string;
  userId?: string;
  label: string;
  createdAt: string;
  lastSeenAt?: string;
  revokedAt?: string;
  active: boolean;
}

export interface DeviceCreated {
  device: Device;
  token: string;
}

export interface Client {
  id: string;
  label: string;
  createdAt: string;
  lastSeenAt?: string;
  active: boolean;
}

export type WorkspaceRole = "admin" | "member";

export interface Workspace {
  id: string;
  name: string;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

// Mirrors manager.WorkspaceMembershipView: a workspace plus the requesting
// client's role in it (returned by GET /api/workspaces for non-admins and in
// /api/me).
export interface WorkspaceMembershipView extends Workspace {
  role: WorkspaceRole;
}

export interface Membership {
  workspaceId: string;
  clientId?: string;
  clientLabel?: string;
  role: WorkspaceRole;
  createdAt: string;
  // Website-user attribution; may be absent for legacy client-only members.
  userId?: string;
  username?: string;
}

export interface WorkspaceInvite {
  id: string;
  workspaceId: string;
  workspaceName?: string;
  role: WorkspaceRole;
  createdBy?: string;
  createdAt: string;
  expiresAt: string;
  revokedAt?: string;
  usedCount: number;
  lastUsedAt?: string;
  valid: boolean;
}

export interface WorkspaceInviteCreated {
  invite: WorkspaceInvite;
  token: string;
  url?: string;
}

export interface InvitePreview {
  valid: boolean;
  workspaceId: string;
  workspaceName: string;
  role: WorkspaceRole;
  expiresAt: string;
}

export interface RefreshQueueItem {
  accountId: string;
  label: string;
  status: AccountStatus;
  authPresent: boolean;
  updatedAt?: string;
  resetsAt?: string;
  remainingDisplay?: string;
  remainingPercent?: number;
  usedPercent?: number;
  fiveHourResetsAt?: string;
  fiveHourRemainingDisplay?: string;
  fiveHourRemainingPercent?: number;
  fiveHourUsedPercent?: number;
  sevenDayResetsAt?: string;
  sevenDayRemainingDisplay?: string;
  sevenDayRemainingPercent?: number;
  sevenDayUsedPercent?: number;
  bindingWindow?: string;
  quotaStatus?: string;
  refreshOrderReason?: string;
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  quotaSource?: string;
  quotaReporterClientId?: string;
  leaseActive?: boolean;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
}

export interface LoadBalanceAccount {
  id: string;
  label: string;
  status: AccountStatus;
  authPresent: boolean;
  configPresent: boolean;
  active: boolean;
  codexHome: string;
  workspaceId?: string;
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  generation?: number;
  leaseActive?: boolean;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
  eligible: boolean;
  reason?: string;
  runtimeState?: string;
  runtimeReason?: string;
  quotaStatus?: string;
  quotaRemainingDisplay?: string;
  quotaRemainingPercent?: number;
  quotaUsedPercent?: number;
  quotaResetsAt?: string;
  quotaUpdatedAt?: string;
  quotaScore?: number;
  quotaSevenDayRemainingDisplay?: string;
  quotaSevenDayRemainingPercent?: number;
  quotaSevenDayUsedPercent?: number;
  quotaSevenDayResetsAt?: string;
  quotaBindingWindow?: string;
}

export interface LoadBalanceStatus {
  policy: string;
  statePath: string;
  lastAccountId: string;
  eligible: LoadBalanceAccount[];
  excluded: LoadBalanceAccount[];
}

export interface PersonalPayload {
  mode: "admin" | "client" | "user";
  admin: boolean;
  user?: User;
  client?: Client;
  clients?: Client[];
  devices?: Device[];
  workspaces?: WorkspaceMembershipView[];
  usage?: AccountUsage[] | Record<string, AccountUsage>;
  dispatches?: DispatchEvent[];
  totals?: {
    today: UsageToken;
    sevenDays: UsageToken;
    allTime: UsageToken;
  };
  refreshQueue?: RefreshQueueItem[];
}

export type AccessMode = "unknown" | "admin" | "personal";
export type DashboardView = "accounts" | "load-balancer" | "people" | "import" | "overview" | "workspaces" | "devices" | "audit";
