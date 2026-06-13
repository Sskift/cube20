// Row normalization for the load-balancer pool table, plus the derived overview
// items / alerts / quota-pressure helpers. The accounts page renders its own
// inventory table from Account[] directly; this module is dispatch-focused and
// maps LoadBalanceAccount into the scannable AccountRow shape.

import {
  accountStatusColor,
  clampUIPercent,
  lbAccountName,
  quotaStatusLabel,
} from "./format";
import type {
  AccountStatus,
  ChipColor,
  DispatchEvent,
  LoadBalanceAccount,
  LoadBalanceStatus,
  QuotaResult,
  RefreshQueueItem,
  TranslateFn,
} from "../types";

export interface AccountRow {
  id: string;
  name: string;
  status: AccountStatus;
  statusColor: ChipColor;
  authPresent: boolean;
  ownerMode: string;
  generation: number;
  active: boolean;
  // pool membership (lb view); undefined when not applicable
  eligible?: boolean;
  // quota
  quotaLabel: string;
  quotaPercent: number;
  quotaColor: ChipColor;
  resetsAt?: string;
  bindingWindow?: string;
  sevenDayPercent?: number;
  sevenDayLabel?: string;
  sevenDayResetsAt?: string;
  // lease
  leaseActive: boolean;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
  leaseKind?: string;
  // scoring / reasons
  score?: number;
  reason?: string;
  runtimeState?: string;
  runtimeReason?: string;
  quotaSource?: string;
  // raw refs for the expand detail
  dispatch?: DispatchEvent;
  refresh?: RefreshQueueItem;
  quota?: QuotaResult;
}

// remaining-percent -> chip colour, the single source of truth for quota tone.
export function remainingColor(percent: number | undefined, ok: boolean): ChipColor {
  if (!ok) return "default";
  const value = clampUIPercent(percent);
  if (value <= 20) return "danger";
  if (value <= 45) return "warning";
  return "success";
}

// Build rows for the load-balancer view from LoadBalanceStatus.
export function buildLbRows(
  lb: LoadBalanceStatus | null,
  dispatchByAccount: Map<string, DispatchEvent>,
  t: TranslateFn,
): AccountRow[] {
  if (!lb) return [];
  const all = [...lb.eligible, ...lb.excluded];
  return all.map((account) => lbRow(account, dispatchByAccount.get(account.id), t));
}

function lbRow(account: LoadBalanceAccount, dispatch: DispatchEvent | undefined, t: TranslateFn): AccountRow {
  const percent = clampUIPercent(account.quotaRemainingPercent);
  const quotaLabel = account.quotaRemainingDisplay || quotaStatusLabel(account.quotaStatus, t);
  return {
    id: account.id,
    name: lbAccountName(account),
    status: account.status,
    statusColor: accountStatusColor(account.status),
    authPresent: account.authPresent,
    ownerMode: account.ownerMode || "cloud",
    generation: account.generation || 0,
    active: account.active,
    eligible: account.eligible,
    quotaLabel,
    quotaPercent: percent,
    quotaColor: remainingColor(account.quotaRemainingPercent, account.quotaStatus === "supported"),
    resetsAt: account.quotaResetsAt,
    bindingWindow: account.quotaBindingWindow,
    sevenDayPercent: account.quotaSevenDayRemainingPercent,
    sevenDayLabel: account.quotaSevenDayRemainingDisplay,
    sevenDayResetsAt: account.quotaSevenDayResetsAt,
    leaseActive: !!account.leaseActive,
    leaseClientId: account.leaseClientId,
    leaseHolder: account.leaseHolder,
    leaseExpiresAt: account.leaseExpiresAt,
    leaseKind: account.leaseKind,
    score: account.quotaScore,
    reason: account.reason,
    runtimeState: account.runtimeState,
    runtimeReason: account.runtimeReason,
    dispatch,
  };
}

// ---- Overview-bar items + alerts ----------------------------------------

export type Tone = "neutral" | "success" | "warning" | "danger" | "accent";

export interface OverviewItem {
  key: string;
  label: string;
  value: string;
  sub?: string;
  tone: Tone;
}

export interface AlertEntry {
  id: string;
  name: string;
  reason: string;
  tone: Tone;
}

// An account is "under pressure" if it is in-pool but low on quota or close to
// its 5h reset — the operator wants these surfaced before they drain.
export function quotaPressure(rows: AccountRow[], nowMs: number): AccountRow[] {
  return rows.filter((row) => {
    if (row.eligible === false) return false;
    if (row.quotaColor === "danger") return true;
    if (!row.resetsAt) return false;
    const target = new Date(row.resetsAt).getTime();
    if (Number.isNaN(target)) return false;
    const minutes = (target - nowMs) / 60_000;
    return minutes > 0 && minutes <= 90;
  });
}

// Alerts = excluded-from-pool accounts (with their reason) plus any account whose
// stored auth needs attention. These map directly to "account anomaly/exclusion
// reason", one of the three core信息 the operator asked to see first.
export function buildAlerts(rows: AccountRow[], t: TranslateFn): AlertEntry[] {
  const alerts: AlertEntry[] = [];
  for (const row of rows) {
    if (!row.authPresent) {
      alerts.push({ id: row.id, name: row.name, reason: t("缺少凭据", "auth missing"), tone: "danger" });
      continue;
    }
    if (row.eligible === false && row.reason) {
      alerts.push({ id: row.id, name: row.name, reason: row.reason, tone: "warning" });
    }
  }
  return alerts;
}

export function toneToChipColor(tone: Tone): ChipColor {
  switch (tone) {
    case "success":
      return "success";
    case "warning":
      return "warning";
    case "danger":
      return "danger";
    case "accent":
      return "accent";
    default:
      return "default";
  }
}
