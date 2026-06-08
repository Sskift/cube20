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
}

export interface Client {
  id: string;
  label: string;
  createdAt: string;
  lastSeenAt?: string;
  active: boolean;
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
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  generation?: number;
  leaseActive?: boolean;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
  eligible: boolean;
  reason?: string;
  quotaStatus?: string;
  quotaRemainingDisplay?: string;
  quotaRemainingPercent?: number;
  quotaUsedPercent?: number;
  quotaResetsAt?: string;
  quotaUpdatedAt?: string;
  quotaScore?: number;
}

export interface LoadBalanceStatus {
  policy: string;
  statePath: string;
  lastAccountId: string;
  eligible: LoadBalanceAccount[];
  excluded: LoadBalanceAccount[];
}

export interface PersonalPayload {
  mode: "admin" | "client";
  admin: boolean;
  client?: Client;
  clients?: Client[];
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
export type DashboardView = "accounts" | "load-balancer" | "people" | "import" | "overview";
