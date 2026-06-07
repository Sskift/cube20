import { useEffect, useMemo, useState } from "react";
import { Card, Chip } from "@heroui/react";
import { Activity, Clock, Gauge } from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

interface QuotaRow {
  accountId: string;
  label?: string;
  status?: string;
  usedPercent?: number;
  remainingPercent?: number;
  resetsAt?: string;
  leaseClientId?: string;
  leaseActive?: boolean;
  quotaSource?: string;
}

interface TimelinePoint {
  accountId: string;
  name: string;
  minutes: number;
  resetsAt?: string;
}

function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}...${value.slice(-4)}` : value;
}

function rowName(row: QuotaRow) {
  return row.label || shortID(row.accountId);
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

function minutesUntil(value?: string) {
  if (!value) return undefined;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return undefined;
  return Math.max(0, (date.getTime() - Date.now()) / 60000);
}

function usageColor(used?: number): "success" | "warning" | "danger" | "default" {
  if (typeof used !== "number" || Number.isNaN(used)) return "default";
  if (used > 80) return "danger";
  if (used > 55) return "warning";
  return "success";
}

function sourceLabel(value?: string) {
  switch (value) {
    case "client":
      return "客户端上报";
    case "cloud":
      return "云端读取";
    default:
      return "—";
  }
}

function fmtMinutes(value: number) {
  if (value < 1) return "现在";
  if (value < 60) return `${Math.round(value)} 分钟`;
  const hours = Math.floor(value / 60);
  const mins = Math.round(value % 60);
  return mins ? `${hours} 小时 ${mins} 分` : `${hours} 小时`;
}

function TimelineTooltip({
  active,
  payload,
}: {
  active?: boolean;
  payload?: Array<{ payload: TimelinePoint }>;
}) {
  if (!active || !payload?.length) return null;
  const point = payload[0].payload;
  return (
    <div className="rounded-lg border border-slate-200 bg-surface px-3 py-2 text-xs shadow-lg">
      <div className="font-semibold text-slate-950">{point.name}</div>
      <div className="mt-1 text-slate-500">距刷新 {fmtMinutes(point.minutes)}</div>
      <div className="text-slate-400">{fmtTime(point.resetsAt)}</div>
    </div>
  );
}

function useChartTheme() {
  const [theme, setTheme] = useState(0); // bump to force re-read on theme change
  useEffect(() => {
    const root = document.documentElement;
    const observer = new MutationObserver(() => setTheme((n) => n + 1));
    observer.observe(root, { attributes: true, attributeFilter: ["class", "data-theme"] });
    return () => observer.disconnect();
  }, []);
  return useMemo(() => {
    if (typeof window === "undefined") {
      return { grid: "#e2e8f0", axis: "#64748b", bar: "#6366f1", cursor: "rgba(99,102,241,0.10)" };
    }
    const styles = getComputedStyle(document.documentElement);
    const read = (name: string, fallback: string) => styles.getPropertyValue(name).trim() || fallback;
    return {
      grid: read("--border", "#e2e8f0"),
      axis: read("--muted", "#64748b"),
      bar: read("--accent", "#6366f1"),
      cursor: "color-mix(in oklab, var(--accent) 12%, transparent)",
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [theme]);
}

export function QuotaOverview({ queue }: { queue: QuotaRow[] }) {
  const rows = useMemo(() => queue ?? [], [queue]);
  const chart = useChartTheme();

  const timeline = useMemo(() => {
    const points: TimelinePoint[] = [];
    for (const row of rows) {
      const minutes = minutesUntil(row.resetsAt);
      if (minutes === undefined) continue;
      points.push({
        accountId: row.accountId,
        name: rowName(row),
        minutes: Math.round(minutes * 10) / 10,
        resetsAt: row.resetsAt,
      });
    }
    points.sort((a, b) => a.minutes - b.minutes);
    return points;
  }, [rows]);

  const chartHeight = Math.max(160, timeline.length * 36);
  const leasedCount = rows.filter((row) => row.leaseActive).length;

  return (
    <div className="grid grid-cols-1 gap-4">
      <Card className="cube-card overflow-hidden">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <div className="min-w-0">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Gauge size={17} />
              配额总览
            </h2>
            <p className="text-xs text-slate-500">所有账号 5h 配额用量与租用状态。</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Chip color="accent" variant="soft">
              {rows.length} 账号
            </Chip>
            <Chip color={leasedCount ? "accent" : "default"} variant="soft">
              {leasedCount} 租用中
            </Chip>
          </div>
        </Card.Header>
        <Card.Content className="p-0">
          {rows.length ? (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[720px] border-collapse text-sm">
                <thead>
                  <tr className="border-b border-slate-200 text-left text-xs font-semibold uppercase text-slate-500">
                    <th className="px-4 py-3 font-semibold">账号</th>
                    <th className="px-4 py-3 font-semibold">5h 用量%</th>
                    <th className="px-4 py-3 font-semibold">5h 剩余%</th>
                    <th className="px-4 py-3 font-semibold">刷新时间</th>
                    <th className="px-4 py-3 font-semibold">租用者</th>
                    <th className="px-4 py-3 font-semibold">状态</th>
                    <th className="px-4 py-3 font-semibold">来源</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {rows.map((row) => (
                    <tr key={row.accountId} className="text-slate-700 hover:bg-slate-50">
                      <td className="px-4 py-3">
                        <div className="font-semibold text-slate-950">{rowName(row)}</div>
                        <div className="mt-0.5 font-mono text-xs text-slate-400">{shortID(row.accountId)}</div>
                      </td>
                      <td className="px-4 py-3">
                        <Chip color={usageColor(row.usedPercent)} size="sm" variant="soft">
                          {fmtPercent(row.usedPercent)}
                        </Chip>
                      </td>
                      <td className="px-4 py-3 font-medium text-slate-900">{fmtPercent(row.remainingPercent)}</td>
                      <td className="px-4 py-3 text-slate-600">{fmtTime(row.resetsAt)}</td>
                      <td className="px-4 py-3">
                        {row.leaseClientId ? (
                          <span className="font-mono text-xs text-slate-700">{row.leaseClientId}</span>
                        ) : (
                          <span className="text-slate-400">—</span>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <Chip color={row.status === "ready" ? "success" : "warning"} size="sm" variant="soft">
                          {row.status || "—"}
                        </Chip>
                      </td>
                      <td className="px-4 py-3 text-slate-600">{sourceLabel(row.quotaSource)}</td>
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
              <div className="text-sm font-semibold text-slate-950">暂无配额数据</div>
              <div className="mt-1 max-w-sm text-xs text-slate-500">导入账号并完成配额检查后，这里会显示 5h 用量总览。</div>
            </div>
          )}
        </Card.Content>
      </Card>

      <Card className="cube-card overflow-hidden">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <Activity size={17} />
            刷新时间轴
          </h2>
          <Chip color="accent" variant="soft">
            {timeline.length} 待刷新
          </Chip>
        </Card.Header>
        <Card.Content>
          {timeline.length ? (
            <div className="w-full">
              <ResponsiveContainer width="100%" height={chartHeight}>
                <BarChart
                  data={timeline}
                  layout="vertical"
                  margin={{ top: 8, right: 24, bottom: 8, left: 12 }}
                >
                  <CartesianGrid horizontal={false} stroke={chart.grid} strokeDasharray="3 3" />
                  <XAxis
                    type="number"
                    dataKey="minutes"
                    tick={{ fontSize: 11, fill: chart.axis }}
                    label={{ value: "距刷新(分钟)", position: "insideBottom", offset: -2, fontSize: 11, fill: chart.axis }}
                  />
                  <YAxis
                    type="category"
                    dataKey="name"
                    width={140}
                    tick={{ fontSize: 11, fill: chart.axis }}
                  />
                  <Tooltip content={<TimelineTooltip />} cursor={{ fill: chart.cursor }} />
                  <Bar dataKey="minutes" fill={chart.bar} radius={[0, 4, 4, 0]} barSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <div className="flex items-center gap-2 rounded-lg bg-slate-50 px-4 py-6 text-sm text-slate-500">
              <Clock size={16} className="shrink-0" />
              暂无可计算的刷新时间（缺少 5h 重置时间）。
            </div>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}
