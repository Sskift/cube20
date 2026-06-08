// Pure formatters and label helpers shared across the dashboard. Functions that
// produce user-facing text take a TranslateFn (t) parameter — they cannot call
// the useLang hook because they run outside React components.

import type {
  Account,
  ChipColor,
  LoadBalanceAccount,
  QuotaResult,
  RefreshQueueItem,
  TranslateFn,
} from "../types";

export function maskSecret(value: string) {
  if (!value) return "-";
  if (value.length <= 14) return `${value.slice(0, 3)}...`;
  return `${value.slice(0, 10)}...${value.slice(-6)}`;
}

export async function copyText(value: string) {
  if (!value || typeof navigator === "undefined" || !navigator.clipboard) return;
  await navigator.clipboard.writeText(value);
}

export function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}...${value.slice(-4)}` : value;
}

export function tokens(value?: number) {
  if (!value) return "0";
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return Math.round(value).toString();
}

export function shortTime(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// Compact relative countdown to a reset/expiry timestamp, e.g. "2h 13m" or
// "12m". Returns "now" once the moment has passed and "-" when unparseable.
// `nowMs` is injected so callers can drive it from a ticking clock.
export function countdown(value: string | undefined, nowMs: number): string {
  if (!value) return "-";
  const target = new Date(value).getTime();
  if (Number.isNaN(target)) return "-";
  const diff = target - nowMs;
  if (diff <= 0) return "now";
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const rem = minutes % 60;
  if (hours < 24) return rem ? `${hours}h ${rem}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

export function accountName(account?: Account) {
  if (!account) return "-";
  return account.label || shortID(account.id);
}

export function clampUIPercent(value?: number) {
  if (typeof value !== "number" || Number.isNaN(value)) return 0;
  return Math.max(0, Math.min(100, value));
}

export function lbAccountName(account: LoadBalanceAccount) {
  return account.label || shortID(account.id);
}

export function scoreLabel(value?: number) {
  if (typeof value !== "number" || Number.isNaN(value)) return "-";
  return value.toFixed(1);
}

export interface QuotaSummary {
  label: string;
  value: number;
  color: ChipColor;
}

export function quotaSummary(quota: QuotaResult | undefined, t: TranslateFn): QuotaSummary {
  if (!quota) return { label: t("未检查", "Not checked"), value: 0, color: "default" };
  if (quota.status === "loading") return { label: t("检查中", "Checking"), value: 0, color: "accent" };
  if (quota.status === "supported" && quota.quotas?.length) {
    const primary = quota.quotas[0];
    return {
      label: `${primary.remainingDisplay} ${t("剩余", "left")}`,
      value: Math.max(0, Math.min(100, 100 - primary.usedPercent)),
      color: primary.usedPercent > 80 ? "danger" : primary.usedPercent > 55 ? "warning" : "success",
    };
  }
  if (quota.status === "unsupported_api_key") return { label: t("API 密钥", "API key"), value: 0, color: "warning" };
  if (quota.status === "refresh_token_invalidated") return { label: t("需重新登录", "Re-login"), value: 0, color: "danger" };
  return { label: quota.status, value: 0, color: "default" };
}

export function quotaHint(quota: QuotaResult | undefined, t: TranslateFn) {
  if (!quota) return "";
  if (quota.status === "refresh_token_invalidated") return t("存储的令牌已被轮换或撤销。请重新登录该账号,或上传新的 auth.json。", "Stored token was rotated or revoked. Re-login this account or upload a fresh auth.json.");
  if (quota.status === "unsupported_api_key") return t("API 密钥鉴权无法获取订阅余额。", "API-key auth cannot expose subscription balance.");
  if (quota.status === "not_configured") return t("缺少 auth.json。", "auth.json is missing.");
  if (quota.status === "error") return quota.detail || t("配额检查失败。", "Quota check failed.");
  return "";
}

export function quotaStatusLabel(value: string | undefined, t: TranslateFn) {
  switch (value) {
    case "refresh_token_invalidated":
      return t("需重新登录", "re-login");
    case "unsupported_api_key":
      return t("API 密钥", "api key");
    case "not_configured":
      return t("缺失", "missing");
    case "supported":
      return t("已检查", "checked");
    case "error":
      return t("错误", "error");
    default:
      return value || "-";
  }
}

export function dispatchEventLabel(event: string | undefined, t: TranslateFn) {
  switch (event) {
    case "claimed":
      return t("已派发", "dispatched");
    case "released":
      return t("已释放", "released");
    case "expired":
      return t("已过期", "expired");
    default:
      return event || "-";
  }
}

export function dispatchTarget(clientId?: string, clientLabel?: string, holder?: string) {
  const label = clientLabel?.trim() || holder?.trim() || clientId?.trim();
  if (!label) return "-";
  if (clientId && label !== clientId) return `${label} · ${shortID(clientId)}`;
  return label;
}

export function refreshQueueReason(item: RefreshQueueItem, t: TranslateFn) {
  if (item.ownerMode === "client") {
    return `${item.refreshOrderReason || t("客户端上报", "client reported")}${item.quotaReporterClientId ? ` · ${item.quotaReporterClientId}` : ""}`;
  }
  if (item.leaseActive) return `${t("租用方", "leased by")} ${item.leaseClientId || "client"}`;
  return item.refreshOrderReason || item.quotaStatus || item.status;
}

// Status -> chip colour, shared by every account/lease badge.
export function accountStatusColor(status: Account["status"]): ChipColor {
  if (status === "ready") return "success";
  if (status === "recovering" || status === "drain") return "warning";
  return "danger";
}
