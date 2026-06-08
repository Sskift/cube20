import type { ReactNode } from "react";
import { Chip } from "@heroui/react";

import type { OverviewItem } from "../lib/rows";
import { toneToChipColor } from "../lib/rows";

// A compact, horizontally-scannable strip of the few numbers that matter at a
// glance: pool readiness, the next dispatch candidate, alert count, quota
// pressure. Replaces the old wall of MetricCards + duplicated summary boxes.
export function OverviewBar({ items }: { items: OverviewItem[] }) {
  return (
    <div className="cube-overview">
      {items.map((item) => (
        <div key={item.key} className={`cube-overview-cell tone-${item.tone}`}>
          <div className="cube-overview-label">{item.label}</div>
          <div className="cube-overview-value">{item.value}</div>
          {item.sub && <div className="cube-overview-sub">{item.sub}</div>}
        </div>
      ))}
    </div>
  );
}

// The single most important card on the screen: who cube run will dispatch next
// and why. Rendered prominently at the top of both the first screen and the LB
// page.
export function NextAccountCard({
  name,
  detail,
  score,
  reset,
  empty,
  emptyHint,
  scoreLabel,
  resetLabel,
  ring,
}: {
  name: string;
  detail?: string;
  score?: string;
  reset?: string;
  empty: boolean;
  emptyHint: string;
  scoreLabel: string;
  resetLabel: string;
  ring?: ReactNode;
}) {
  if (empty) {
    return (
      <div className="cube-next is-empty">
        <div className="cube-next-title">{emptyHint}</div>
      </div>
    );
  }
  return (
    <div className="cube-next">
      {ring}
      <div className="min-w-0 flex-1">
        <div className="cube-next-name">{name}</div>
        {detail && <div className="cube-next-detail">{detail}</div>}
        <div className="cube-next-stats">
          {score !== undefined && (
            <span>
              <strong>{scoreLabel}</strong> {score}
            </span>
          )}
          {reset !== undefined && (
            <span>
              <strong>{resetLabel}</strong> {reset}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

// Account anomalies / exclusion reasons — the third core信息. Kept terse; one
// line per troubled account. Empty state reads as "all healthy".
export function AlertsPanel({
  alerts,
  emptyLabel,
}: {
  alerts: { id: string; name: string; reason: string; tone: "neutral" | "success" | "warning" | "danger" | "accent" }[];
  emptyLabel: string;
}) {
  if (!alerts.length) {
    return <div className="cube-alerts-empty">{emptyLabel}</div>;
  }
  return (
    <div className="cube-alerts">
      {alerts.map((alert) => (
        <div key={alert.id} className={`cube-alert tone-${alert.tone}`}>
          <span className="cube-alert-name">{alert.name}</span>
          <Chip color={toneToChipColor(alert.tone)} size="sm" variant="soft">
            {alert.reason}
          </Chip>
        </div>
      ))}
    </div>
  );
}
