import { Chip } from "@heroui/react";

import { useLang } from "../i18n";
import { dispatchEventLabel, dispatchTarget, shortID, shortTime } from "../lib/format";
import type { DispatchEvent } from "../types";

// Compact dispatch history list, used in the LB page secondary column and the
// personal dashboard. Pure presentational over a slice of DispatchEvent[].
export function DispatchTimeline({ dispatches }: { dispatches: DispatchEvent[] }) {
  const { t } = useLang();
  if (!dispatches.length) {
    return <div className="lb-empty">{t("还没有调度记录。", "No dispatches recorded yet.")}</div>;
  }
  return (
    <div className="dispatch-list">
      {dispatches.map((event) => (
        <div key={event.id} className={`dispatch-row event-${event.event || "unknown"}`}>
          <div className="dispatch-dot" />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center justify-between gap-2">
              <span className="truncate text-sm font-semibold text-slate-950">{event.accountLabel || shortID(event.accountId)}</span>
              <Chip color={event.event === "claimed" ? "success" : event.event === "expired" ? "danger" : "default"} size="sm" variant="soft">
                {dispatchEventLabel(event.event, t)}
              </Chip>
            </div>
            <div className="mt-1 truncate text-xs text-slate-500">
              {t("发往", "to")} {dispatchTarget(event.clientId, event.clientLabel, event.holder)} · {shortTime(event.createdAt)}
            </div>
            <div className="mt-1 font-mono text-[11px] text-slate-400">{shortID(event.leaseId)}</div>
          </div>
        </div>
      ))}
    </div>
  );
}
