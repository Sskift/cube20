import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Card, Chip } from "@heroui/react";
import { Clock, Gauge, GanttChartSquare, LayoutGrid, Timer } from "lucide-react";

import { useLang } from "../i18n";
import { useNow } from "../hooks/useNow";
import type { RefreshQueueItem, TranslateFn } from "../types";

// QuotaOverview renders the pool's quota picture three ways, switchable:
//   1. Recovery Gantt  — per-account lanes on a shared time axis: red = cooling
//      down until its binding window resets, blue = leased until expiry, green =
//      ready now. Answers "who is usable, and who recovers when".
//   2. Dual-window heatmap — per account, the 5h and 7d windows side by side,
//      coloured by remaining %, with the binding (most-constrained) window
//      highlighted. Compact, no time axis.
//   3. Reset timeline — every upcoming window reset merged into one vertical
//      timeline sorted by time, each marked with the account it unlocks.
// All three read the same RefreshQueueItem[] the load balancer evaluates.

type ViewMode = "gantt" | "heatmap" | "timeline";

const VIEW_STORAGE_KEY = "cube-quota-view";

// An account is treated as exhausted at/under this remaining %, matching the
// backend eligibility floor (loadBalanceMinFiveHourRemaining = 5).
const EXHAUSTED_THRESHOLD = 5;

const BAR_FILL: Record<Bucket, string> = {
  success: "#10b981",
  warning: "#f59e0b",
  danger: "#ef4444",
  muted: "#cbd5e1",
};

type Bucket = "success" | "warning" | "danger" | "muted";

interface WindowState {
  label: "5h" | "7d";
  percent?: number;
  display?: string;
  resetMs?: number;
  resetsAt?: string;
  present: boolean;
}

interface Lane {
  accountId: string;
  name: string;
  status?: string;
  source?: string;
  binding?: "5h" | "7d";
  five: WindowState;
  seven: WindowState;
  bindingPercent?: number;
  leaseActive: boolean;
  leaseClientId?: string;
  leaseMs?: number;
  // derived recovery state
  blockReason: "ready" | "cooldown" | "leased";
  blockedUntilMs?: number;
}

function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}...${value.slice(-4)}` : value;
}

function rowName(row: RefreshQueueItem) {
  return row.label || shortID(row.accountId);
}

// validMs parses an RFC3339 string to epoch ms, rejecting empty strings and the
// Go zero time (year < 2000) that a non-omitted time.Time serializes to.
function validMs(value?: string): number | undefined {
  if (!value) return undefined;
  const ms = new Date(value).getTime();
  if (Number.isNaN(ms)) return undefined;
  if (ms < Date.UTC(2000, 0, 1)) return undefined;
  return ms;
}

// pctOf prefers the numeric percent but falls back to parsing the display string
// — the backend's omitempty drops a literal 0.0, so an exhausted window arrives
// as { remainingPercent: undefined, remainingDisplay: "0%" }.
function pctOf(numeric?: number, display?: string): number | undefined {
  if (typeof numeric === "number" && !Number.isNaN(numeric)) return numeric;
  if (display) {
    const match = display.match(/-?\d+(\.\d+)?/);
    if (match) return parseFloat(match[0]);
  }
  return undefined;
}

function fmtTime(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function fmtPercent(value?: number) {
  if (typeof value !== "number" || Number.isNaN(value)) return "—";
  return `${value.toFixed(1)}%`;
}

// fmtSpan turns a millisecond duration into a compact "3d 4h" / "2h 13m" / "now".
function fmtSpan(ms: number, t: TranslateFn) {
  if (ms <= 0) return t("现在", "now");
  const minutes = Math.round(ms / 60000);
  if (minutes < 60) return `${minutes}${t("分", "m")}`;
  const hours = Math.floor(minutes / 60);
  const mins = minutes % 60;
  if (hours < 24) return mins ? `${hours}${t("时", "h")} ${mins}${t("分", "m")}` : `${hours}${t("时", "h")}`;
  const days = Math.floor(hours / 24);
  const remHours = hours % 24;
  return remHours ? `${days}${t("天", "d")} ${remHours}${t("时", "h")}` : `${days}${t("天", "d")}`;
}

function bucketOf(percent?: number): Bucket {
  if (typeof percent !== "number" || Number.isNaN(percent)) return "muted";
  if (percent <= 20) return "danger";
  if (percent <= 45) return "warning";
  return "success";
}

function sourceLabel(value: string | undefined, t: TranslateFn) {
  switch (value) {
    case "client":
      return t("客户端上报", "client-reported");
    case "cloud":
      return t("云端读取", "cloud-read");
    default:
      return "—";
  }
}

function bindingOf(row: RefreshQueueItem, fivePct?: number, sevenPct?: number): "5h" | "7d" | undefined {
  if (row.bindingWindow === "5h" || row.bindingWindow === "7d") return row.bindingWindow;
  if (typeof fivePct === "number" && typeof sevenPct === "number") return sevenPct < fivePct ? "7d" : "5h";
  if (typeof fivePct === "number") return "5h";
  if (typeof sevenPct === "number") return "7d";
  return undefined;
}

function buildLane(row: RefreshQueueItem, now: number): Lane {
  const fivePct = pctOf(row.fiveHourRemainingPercent, row.fiveHourRemainingDisplay);
  const sevenPct = pctOf(row.sevenDayRemainingPercent, row.sevenDayRemainingDisplay);
  const bindingPercent = pctOf(row.remainingPercent, row.remainingDisplay);
  const bindingResetMs = validMs(row.resetsAt);
  const leaseMs = row.leaseActive ? validMs(row.leaseExpiresAt) : undefined;

  const five: WindowState = {
    label: "5h",
    percent: fivePct,
    display: row.fiveHourRemainingDisplay,
    resetMs: validMs(row.fiveHourResetsAt),
    resetsAt: row.fiveHourResetsAt,
    present: fivePct !== undefined || !!validMs(row.fiveHourResetsAt),
  };
  const seven: WindowState = {
    label: "7d",
    percent: sevenPct,
    display: row.sevenDayRemainingDisplay,
    resetMs: validMs(row.sevenDayResetsAt),
    resetsAt: row.sevenDayResetsAt,
    present: sevenPct !== undefined || !!validMs(row.sevenDayResetsAt),
  };

  const exhausted = typeof bindingPercent === "number" && bindingPercent <= EXHAUSTED_THRESHOLD;
  let blockReason: Lane["blockReason"] = "ready";
  let blockedUntilMs: number | undefined;
  if (exhausted && bindingResetMs && bindingResetMs > now) {
    blockReason = "cooldown";
    blockedUntilMs = bindingResetMs;
  } else if (leaseMs && leaseMs > now) {
    blockReason = "leased";
    blockedUntilMs = leaseMs;
  }

  return {
    accountId: row.accountId,
    name: rowName(row),
    status: row.status,
    source: row.quotaSource,
    binding: bindingOf(row, fivePct, sevenPct),
    five,
    seven,
    bindingPercent,
    leaseActive: !!row.leaseActive,
    leaseClientId: row.leaseClientId,
    leaseMs,
    blockReason,
    blockedUntilMs,
  };
}

// ---- Segmented switcher --------------------------------------------------

function Segmented({
  mode,
  onChange,
  t,
}: {
  mode: ViewMode;
  onChange: (mode: ViewMode) => void;
  t: TranslateFn;
}) {
  const options: { key: ViewMode; label: string; icon: typeof GanttChartSquare }[] = [
    { key: "gantt", label: t("恢复甘特图", "Recovery Gantt"), icon: GanttChartSquare },
    { key: "heatmap", label: t("双窗口热力", "Dual-window"), icon: LayoutGrid },
    { key: "timeline", label: t("重置时间线", "Reset timeline"), icon: Timer },
  ];
  return (
    <div className="flex gap-1 rounded-xl border border-slate-200 bg-surface p-1 shadow-sm">
      {options.map((option) => {
        const active = mode === option.key;
        const Icon = option.icon;
        return (
          <button
            key={option.key}
            aria-current={active ? "true" : undefined}
            className={`flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${
              active ? "cube-brand shadow-sm" : "text-slate-600 hover:bg-slate-100 hover:text-slate-950"
            }`}
            type="button"
            onClick={() => onChange(option.key)}
          >
            <Icon size={14} />
            <span className="hidden sm:inline">{option.label}</span>
          </button>
        );
      })}
    </div>
  );
}

// ---- View 1: Recovery Gantt swimlanes ------------------------------------

function GanttView({ lanes, now, t }: { lanes: Lane[]; now: number; t: TranslateFn }) {
  const blocked = lanes.filter((lane) => lane.blockedUntilMs && lane.blockedUntilMs > now);
  const spanMs = blocked.reduce((max, lane) => Math.max(max, (lane.blockedUntilMs as number) - now), 0);

  const sorted = useMemo(() => {
    return [...lanes].sort((a, b) => {
      const ra = a.blockReason === "ready" ? 0 : 1;
      const rb = b.blockReason === "ready" ? 0 : 1;
      if (ra !== rb) return ra - rb;
      return (a.blockedUntilMs ?? 0) - (b.blockedUntilMs ?? 0);
    });
  }, [lanes]);

  if (!lanes.length) {
    return <EmptyHint icon={<GanttChartSquare size={16} />} text={t("暂无账号数据。", "No account data yet.")} />;
  }

  const ticks = spanMs > 0 ? [0, 0.25, 0.5, 0.75, 1] : [];

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <LegendDot color={BAR_FILL.success} label={t("就绪", "ready")} />
        <LegendDot color={BAR_FILL.danger} label={t("配额冷却", "cooling down")} />
        <LegendDot color="#3b82f6" label={t("租用中", "leased")} />
      </div>

      {ticks.length > 0 && (
        <div className="flex items-stretch gap-3 pl-[160px] text-[10px] text-slate-400">
          <div className="relative h-4 flex-1">
            {ticks.map((frac) => (
              <span
                key={frac}
                className="absolute -translate-x-1/2 whitespace-nowrap"
                style={{ left: `${frac * 100}%` }}
              >
                {frac === 0 ? t("现在", "now") : `+${fmtSpan(spanMs * frac, t)}`}
              </span>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        {sorted.map((lane) => {
          const wait = lane.blockedUntilMs ? lane.blockedUntilMs - now : 0;
          const widthPct = spanMs > 0 && wait > 0 ? Math.max(3, (wait / spanMs) * 100) : 0;
          const fill = lane.blockReason === "cooldown" ? BAR_FILL.danger : lane.blockReason === "leased" ? "#3b82f6" : BAR_FILL.success;
          return (
            <div key={lane.accountId} className="flex items-center gap-3">
              <div className="w-[148px] shrink-0 truncate text-xs">
                <span className="font-semibold text-slate-900">{lane.name}</span>
                {lane.binding && <span className="ml-1 text-slate-400">· {lane.binding}</span>}
              </div>
              <div className="relative h-7 flex-1 overflow-hidden rounded-md bg-slate-100">
                {lane.blockReason === "ready" ? (
                  <div className="absolute inset-y-0 left-0 flex items-center gap-1.5 rounded-md px-2.5 text-xs font-medium" style={{ color: BAR_FILL.success }}>
                    <span className="h-2 w-2 rounded-full" style={{ backgroundColor: BAR_FILL.success }} />
                    {t("就绪", "ready now")}
                  </div>
                ) : (
                  <>
                    <div
                      className="absolute inset-y-0 left-0 rounded-md transition-[width]"
                      style={{ width: `${widthPct}%`, backgroundColor: fill, opacity: 0.85 }}
                    />
                    <div className="absolute inset-y-0 left-0 flex items-center px-2.5 text-xs font-medium text-white">
                      {lane.blockReason === "cooldown" ? t("冷却", "cools") : t("租用", "leased")} {fmtSpan(wait, t)}
                    </div>
                  </>
                )}
              </div>
              <div className="hidden w-[112px] shrink-0 text-right text-[11px] text-slate-400 sm:block">
                {lane.blockedUntilMs ? fmtTime(new Date(lane.blockedUntilMs).toISOString()) : t("可用", "available")}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ---- View 2: Dual-window heatmap bars ------------------------------------

function HeatmapView({ lanes, t }: { lanes: Lane[]; t: TranslateFn }) {
  const sorted = useMemo(() => {
    return [...lanes].sort((a, b) => (a.bindingPercent ?? 101) - (b.bindingPercent ?? 101));
  }, [lanes]);

  if (!lanes.length) {
    return <EmptyHint icon={<LayoutGrid size={16} />} text={t("暂无账号数据。", "No account data yet.")} />;
  }

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-3 text-xs text-slate-500">
        <LegendDot color={BAR_FILL.success} label={`> 45% ${t("充足", "healthy")}`} />
        <LegendDot color={BAR_FILL.warning} label={`20–45% ${t("偏低", "low")}`} />
        <LegendDot color={BAR_FILL.danger} label={`≤ 20% ${t("紧张", "critical")}`} />
      </div>
      {sorted.map((lane) => (
        <div key={lane.accountId} className="grid grid-cols-1 items-center gap-2 rounded-lg border border-slate-100 bg-slate-50/50 p-2.5 sm:grid-cols-[148px_1fr_1fr]">
          <div className="min-w-0 truncate text-xs">
            <span className="font-semibold text-slate-900">{lane.name}</span>
            <div className="text-[10px] text-slate-400">{sourceLabel(lane.source, t)}</div>
          </div>
          <HeatBar window={lane.five} binding={lane.binding === "5h"} t={t} />
          <HeatBar window={lane.seven} binding={lane.binding === "7d"} t={t} />
        </div>
      ))}
    </div>
  );
}

function HeatBar({ window, binding, t }: { window: WindowState; binding: boolean; t: TranslateFn }) {
  const present = window.present && typeof window.percent === "number";
  const bucket = bucketOf(window.percent);
  const width = present ? Math.max(2, Math.min(100, window.percent as number)) : 0;
  const display = window.display || (present ? `${Math.round(window.percent as number)}%` : "—");
  return (
    <div className={`rounded-md px-2 py-1.5 ${binding ? "ring-2 ring-accent ring-offset-1" : ""}`}>
      <div className="mb-1 flex items-center justify-between text-[10px]">
        <span className="font-semibold uppercase text-slate-500">
          {window.label}
          {binding && <span className="ml-1 rounded bg-accent-soft px-1 text-accent-soft-foreground">{t("约束", "binding")}</span>}
        </span>
        <span className="font-medium text-slate-700">{display}</span>
      </div>
      <div className="relative h-2.5 w-full overflow-hidden rounded-full bg-slate-200">
        {present ? (
          <div className="absolute inset-y-0 left-0 rounded-full" style={{ width: `${width}%`, backgroundColor: BAR_FILL[bucket] }} />
        ) : (
          <div className="absolute inset-0 flex items-center justify-center text-[9px] text-slate-400">{t("无数据", "no data")}</div>
        )}
      </div>
    </div>
  );
}

// ---- View 3: Reset event timeline ----------------------------------------

interface ResetEvent {
  accountId: string;
  name: string;
  window: "5h" | "7d";
  resetMs: number;
  resetsAt: string;
  unlocks: boolean;
}

function TimelineView({ lanes, now, t }: { lanes: Lane[]; now: number; t: TranslateFn }) {
  const events = useMemo(() => {
    const list: ResetEvent[] = [];
    for (const lane of lanes) {
      const exhausted = typeof lane.bindingPercent === "number" && lane.bindingPercent <= EXHAUSTED_THRESHOLD;
      for (const win of [lane.five, lane.seven]) {
        if (!win.resetMs || win.resetMs <= now || !win.resetsAt) continue;
        list.push({
          accountId: lane.accountId,
          name: lane.name,
          window: win.label,
          resetMs: win.resetMs,
          resetsAt: win.resetsAt,
          unlocks: exhausted && lane.binding === win.label,
        });
      }
    }
    list.sort((a, b) => a.resetMs - b.resetMs);
    return list;
  }, [lanes, now]);

  if (!events.length) {
    return <EmptyHint icon={<Clock size={16} />} text={t("暂无即将到来的重置事件。", "No upcoming reset events.")} />;
  }

  return (
    <div className="relative flex flex-col gap-3 pl-6">
      <span className="absolute bottom-2 left-[7px] top-2 w-px bg-slate-200" aria-hidden />
      {events.map((event) => {
        const dot = event.unlocks ? BAR_FILL.success : event.window === "7d" ? "#8b5cf6" : "#3b82f6";
        return (
          <div key={`${event.accountId}-${event.window}`} className="relative flex items-center gap-3">
            <span
              className="absolute -left-6 top-1/2 h-3.5 w-3.5 -translate-y-1/2 rounded-full border-2 border-surface"
              style={{ backgroundColor: dot }}
              aria-hidden
            />
            <div className="w-[88px] shrink-0">
              <div className="text-sm font-semibold text-slate-950">{fmtSpan(event.resetMs - now, t)}</div>
              <div className="text-[10px] text-slate-400">{fmtTime(event.resetsAt)}</div>
            </div>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-sm font-medium text-slate-900">{event.name}</span>
                <Chip color={event.window === "7d" ? "accent" : "default"} size="sm" variant="soft">
                  {event.window}
                </Chip>
                {event.unlocks && (
                  <Chip color="success" size="sm" variant="soft">
                    {t("解锁", "unlocks")}
                  </Chip>
                )}
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---- Shared bits ---------------------------------------------------------

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: color }} />
      {label}
    </span>
  );
}

function EmptyHint({ icon, text }: { icon: ReactNode; text: string }) {
  return (
    <div className="flex items-center gap-2 rounded-lg bg-slate-50 px-4 py-6 text-sm text-slate-500">
      <span className="shrink-0">{icon}</span>
      {text}
    </div>
  );
}

export function QuotaOverview({ queue }: { queue: RefreshQueueItem[] }) {
  const rows = useMemo(() => queue ?? [], [queue]);
  const { t } = useLang();
  const now = useNow();
  const [mode, setMode] = useState<ViewMode>(() => {
    if (typeof window === "undefined") return "gantt";
    try {
      const stored = window.localStorage.getItem(VIEW_STORAGE_KEY);
      if (stored === "gantt" || stored === "heatmap" || stored === "timeline") return stored;
    } catch {
      // ignore storage access errors
    }
    return "gantt";
  });

  function selectMode(next: ViewMode) {
    setMode(next);
    try {
      window.localStorage.setItem(VIEW_STORAGE_KEY, next);
    } catch {
      // ignore storage write errors
    }
  }

  const lanes = useMemo(() => rows.map((row) => buildLane(row, now)), [rows, now]);
  const leasedCount = lanes.filter((lane) => lane.leaseActive).length;
  const readyCount = lanes.filter((lane) => lane.blockReason === "ready").length;
  const coolingCount = lanes.filter((lane) => lane.blockReason === "cooldown").length;

  const viewMeta: Record<ViewMode, { title: string; hint: string; icon: ReactNode }> = {
    gantt: {
      title: t("恢复甘特图", "Recovery Gantt"),
      hint: t("按时间轴展示每个账号何时恢复可用。", "Per-account recovery on a shared time axis."),
      icon: <GanttChartSquare size={17} />,
    },
    heatmap: {
      title: t("双窗口热力条", "Dual-window heatmap"),
      hint: t("并排对比每个账号的 5h 与 7d 余量，高亮约束窗口。", "5h vs 7d remaining per account, binding window highlighted."),
      icon: <LayoutGrid size={17} />,
    },
    timeline: {
      title: t("重置时间线", "Reset timeline"),
      hint: t("所有即将到来的窗口重置，按时间排序。", "Every upcoming window reset, sorted by time."),
      icon: <Timer size={17} />,
    },
  };

  return (
    <div className="grid grid-cols-1 gap-4">
      <Card className="cube-card overflow-hidden">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <div className="min-w-0">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Gauge size={17} />
              {t("配额总览", "Quota Overview")}
            </h2>
            <p className="text-xs text-slate-500">{t("所有账号 5h / 7d 配额用量与租用状态。", "5h / 7d quota usage and lease status across all accounts.")}</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Chip color="accent" variant="soft">
              {rows.length} {t("账号", "accounts")}
            </Chip>
            <Chip color={readyCount ? "success" : "default"} variant="soft">
              {readyCount} {t("就绪", "ready")}
            </Chip>
            <Chip color={leasedCount ? "accent" : "default"} variant="soft">
              {leasedCount} {t("租用中", "leased")}
            </Chip>
          </div>
        </Card.Header>
        <Card.Content className="p-0">
          {rows.length ? (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px] border-collapse text-sm">
                <thead>
                  <tr className="border-b border-slate-200 text-left text-xs font-semibold uppercase text-slate-500">
                    <th className="px-4 py-3 font-semibold">{t("账号", "Account")}</th>
                    <th className="px-4 py-3 font-semibold">{t("约束", "Binding")}</th>
                    <th className="px-4 py-3 font-semibold">{t("5h 剩余%", "5h left %")}</th>
                    <th className="px-4 py-3 font-semibold">{t("7d 剩余%", "7d left %")}</th>
                    <th className="px-4 py-3 font-semibold">{t("刷新时间", "Reset time")}</th>
                    <th className="px-4 py-3 font-semibold">{t("租用者", "Lessee")}</th>
                    <th className="px-4 py-3 font-semibold">{t("状态", "Status")}</th>
                    <th className="px-4 py-3 font-semibold">{t("来源", "Source")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {lanes.map((lane) => (
                    <tr key={lane.accountId} className="text-slate-700 hover:bg-slate-50">
                      <td className="px-4 py-3">
                        <div className="font-semibold text-slate-950">{lane.name}</div>
                        <div className="mt-0.5 font-mono text-xs text-slate-400">{shortID(lane.accountId)}</div>
                      </td>
                      <td className="px-4 py-3">
                        {lane.binding ? (
                          <Chip color={bucketChip(lane.bindingPercent)} size="sm" variant="soft">
                            {lane.binding} · {fmtPercent(lane.bindingPercent)}
                          </Chip>
                        ) : (
                          <span className="text-slate-400">—</span>
                        )}
                      </td>
                      <td className="px-4 py-3 font-medium text-slate-900">{fmtPercent(lane.five.percent)}</td>
                      <td className="px-4 py-3 font-medium text-slate-900">{fmtPercent(lane.seven.percent)}</td>
                      <td className="px-4 py-3 text-slate-600">{fmtTime(lane.five.resetsAt || lane.seven.resetsAt)}</td>
                      <td className="px-4 py-3">
                        {lane.leaseClientId ? (
                          <span className="font-mono text-xs text-slate-700">{lane.leaseClientId}</span>
                        ) : (
                          <span className="text-slate-400">—</span>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <Chip color={lane.status === "ready" ? "success" : "warning"} size="sm" variant="soft">
                          {lane.status || "—"}
                        </Chip>
                      </td>
                      <td className="px-4 py-3 text-slate-600">{sourceLabel(lane.source, t)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center px-4 py-12 text-center">
              <div className="mb-3 grid h-12 w-12 place-items-center rounded-lg bg-slate-100 text-slate-500">
                <Gauge size={24} />
              </div>
              <div className="text-sm font-semibold text-slate-950">{t("暂无配额数据", "No quota data yet")}</div>
              <div className="mt-1 max-w-sm text-xs text-slate-500">{t("导入账号并完成配额检查后，这里会显示配额总览。", "Once accounts are imported and quota checks complete, the quota overview will appear here.")}</div>
            </div>
          )}
        </Card.Content>
      </Card>

      <Card className="cube-card overflow-hidden">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <div className="min-w-0">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              {viewMeta[mode].icon}
              {viewMeta[mode].title}
            </h2>
            <p className="text-xs text-slate-500">{viewMeta[mode].hint}</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {mode === "gantt" && coolingCount > 0 && (
              <Chip color="danger" variant="soft">
                {coolingCount} {t("冷却中", "cooling")}
              </Chip>
            )}
            <Segmented mode={mode} onChange={selectMode} t={t} />
          </div>
        </Card.Header>
        <Card.Content>
          {mode === "gantt" && <GanttView lanes={lanes} now={now} t={t} />}
          {mode === "heatmap" && <HeatmapView lanes={lanes} t={t} />}
          {mode === "timeline" && <TimelineView lanes={lanes} now={now} t={t} />}
        </Card.Content>
      </Card>
    </div>
  );
}

function bucketChip(percent?: number): "success" | "warning" | "danger" | "default" {
  const bucket = bucketOf(percent);
  return bucket === "muted" ? "default" : bucket;
}
