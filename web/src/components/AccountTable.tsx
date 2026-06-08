import { useState } from "react";
import { Chip } from "@heroui/react";
import { ChevronDown } from "lucide-react";

import { useLang } from "../i18n";
import { useNow } from "../hooks/useNow";
import {
  countdown,
  dispatchEventLabel,
  dispatchTarget,
  scoreLabel,
  shortID,
  shortTime,
} from "../lib/format";
import type { AccountRow } from "../lib/rows";

// One scannable table for the whole account pool. Columns: health, quota (bar +
// label), 5h reset countdown, lease, reason, score. Clicking a row expands a
// secondary detail panel (real-time lease holder, dispatch history, source) —
// the info the user explicitly wanted OFF the first screen. On narrow viewports
// the grid collapses to stacked cards via CSS, no separate mobile component.

export function AccountTable({
  rows,
  showScore = false,
  showPool = false,
  selectedId,
  onSelect,
}: {
  rows: AccountRow[];
  showScore?: boolean;
  showPool?: boolean;
  selectedId?: string;
  onSelect?: (id: string) => void;
}) {
  const { t } = useLang();
  const [expanded, setExpanded] = useState<string>("");
  const now = useNow();

  if (!rows.length) {
    return <div className="cube-table-empty">{t("没有账号。", "No accounts.")}</div>;
  }

  function toggle(id: string) {
    setExpanded((current) => (current === id ? "" : id));
    onSelect?.(id);
  }

  return (
    <div className={`cube-table ${showScore ? "with-score" : ""} ${showPool ? "with-pool" : ""}`} role="table">
      <div className="cube-table-head" role="row">
        <span role="columnheader">{t("账号", "Account")}</span>
        <span role="columnheader">{t("配额", "Quota")}</span>
        <span role="columnheader">{t("重置", "Reset")}</span>
        <span role="columnheader">{t("租约", "Lease")}</span>
        {showScore && <span role="columnheader" className="cube-col-score">{t("分数", "Score")}</span>}
        <span role="columnheader" className="cube-col-chevron" aria-hidden="true" />
      </div>

      {rows.map((row) => {
        const isOpen = expanded === row.id;
        const isSelected = selectedId === row.id;
        const reset = countdown(row.resetsAt, now);
        return (
          <div key={row.id} className={`cube-row-wrap ${isOpen ? "is-open" : ""} ${isSelected ? "is-selected" : ""}`}>
            <button
              type="button"
              role="row"
              aria-expanded={isOpen}
              className="cube-row"
              onClick={() => toggle(row.id)}
            >
              <span className="cube-cell cube-cell-account" role="cell">
                <span className={`cube-dot tone-${dotTone(row)}`} aria-hidden="true" />
                <span className="min-w-0">
                  <span className="cube-row-name">
                    {row.name}
                    {row.active && <Chip color="accent" size="sm" variant="soft">{t("活跃", "active")}</Chip>}
                    {showPool && row.eligible !== undefined && (
                      <Chip color={row.eligible ? "success" : "warning"} size="sm" variant="soft">
                        {row.eligible ? t("在池中", "in pool") : t("池外", "out")}
                      </Chip>
                    )}
                  </span>
                  <span className="cube-row-sub">{shortID(row.id)} · {row.status}</span>
                </span>
              </span>

              <span className="cube-cell cube-cell-quota" role="cell">
                <span className="cube-quota-head">
                  <span className="truncate">{row.quotaLabel}</span>
                  <strong className={`tone-${row.quotaColor}`}>{Math.round(row.quotaPercent)}%</strong>
                </span>
                <span className="cube-quota-track">
                  <span className={`tone-${row.quotaColor}`} style={{ width: `${row.quotaPercent}%` }} />
                </span>
              </span>

              <span className="cube-cell cube-cell-reset" role="cell">
                <span className="cube-cell-label">{t("重置", "Reset")}</span>
                <span className="cube-reset-value">{reset}</span>
              </span>

              <span className="cube-cell cube-cell-lease" role="cell">
                <span className="cube-cell-label">{t("租约", "Lease")}</span>
                {row.leaseActive ? (
                  <Chip color="accent" size="sm" variant="soft">{t("租用中", "leased")}</Chip>
                ) : (
                  <span className="cube-muted">{t("空闲", "idle")}</span>
                )}
              </span>

              {showScore && (
                <span className="cube-cell cube-cell-score cube-col-score" role="cell">
                  <span className="cube-cell-label">{t("分数", "Score")}</span>
                  <span className="cube-score-value">{scoreLabel(row.score)}</span>
                </span>
              )}

              <span className="cube-cell cube-col-chevron" role="cell" aria-hidden="true">
                <ChevronDown className={`cube-chevron ${isOpen ? "is-open" : ""}`} size={16} />
              </span>
            </button>

            {row.reason && !isOpen && <div className="cube-row-reason">{row.reason}</div>}

            {isOpen && <RowDetail row={row} now={now} />}
          </div>
        );
      })}
    </div>
  );
}

function RowDetail({ row, now }: { row: AccountRow; now: number }) {
  const { t } = useLang();
  return (
    <div className="cube-row-detail">
      <div className="cube-detail-grid">
        <Detail label={t("归属", "owner")} value={row.ownerMode === "client" ? `client ${row.leaseClientId || "-"}` : "cloud"} />
        <Detail label={t("代次", "gen")} value={String(row.generation)} />
        <Detail label={t("凭据", "auth")} value={row.authPresent ? t("就绪", "ready") : t("缺失", "missing")} />
        <Detail label={t("配额来源", "source")} value={row.quotaSource || "-"} />
        <Detail label={t("5h 重置", "5h reset")} value={row.resetsAt ? `${shortTime(row.resetsAt)} · ${countdown(row.resetsAt, now)}` : "-"} />
        <Detail
          label={t("当前租约", "current lease")}
          value={row.leaseActive ? `${dispatchTarget(row.leaseClientId, "", row.leaseHolder)} · ${t("至", "until")} ${shortTime(row.leaseExpiresAt)}` : "-"}
        />
      </div>
      {row.reason && <div className="cube-detail-reason">{row.reason}</div>}
      {row.dispatch && (
        <div className="cube-detail-dispatch">
          <span className="cube-cell-label">{t("最近调度", "last dispatch")}</span>
          <span>
            {dispatchEventLabel(row.dispatch.event, t)} · {dispatchTarget(row.dispatch.clientId, row.dispatch.clientLabel, row.dispatch.holder)} · {shortTime(row.dispatch.createdAt)}
          </span>
        </div>
      )}
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="cube-detail-cell">
      <span className="cube-cell-label">{label}</span>
      <span className="cube-detail-value">{value}</span>
    </div>
  );
}

// Health dot tone — green ready, amber transitional/excluded, red disabled/no-auth.
function dotTone(row: AccountRow): string {
  if (!row.authPresent || row.status === "disabled") return "danger";
  if (row.eligible === false || row.status === "recovering" || row.status === "drain") return "warning";
  return "success";
}
