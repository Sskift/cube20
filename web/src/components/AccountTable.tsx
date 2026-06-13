import { useState } from "react";
import { Button, Chip } from "@heroui/react";
import { ChevronDown, KeyRound, Undo2 } from "lucide-react";

import { useLang } from "../i18n";
import { useNow } from "../hooks/useNow";
import { isManualLease } from "../lib/manualLease";
import {
  countdown,
  dispatchEventLabel,
  dispatchTarget,
  scoreLabel,
  shortID,
  shortTime,
} from "../lib/format";
import type { AccountRow } from "../lib/rows";
import type { ChipColor, TranslateFn } from "../types";

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
  busy = false,
  liveAuthReady = false,
  onManualBorrow,
  onManualReturn,
}: {
  rows: AccountRow[];
  showScore?: boolean;
  showPool?: boolean;
  selectedId?: string;
  onSelect?: (id: string) => void;
  busy?: boolean;
  liveAuthReady?: boolean;
  onManualBorrow?: (id: string) => void | Promise<void>;
  onManualReturn?: (id: string) => void | Promise<void>;
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
        const runtime = runtimeChip(row, t);
        const rowReason = row.runtimeReason || row.reason;
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
                      <Chip color={runtime.color} size="sm" variant="soft">
                        {runtime.label}
                      </Chip>
                    )}
                  </span>
                  <span className="cube-row-sub">{shortID(row.id)} · {row.status}</span>
                </span>
              </span>

              <span className="cube-cell cube-cell-quota" role="cell">
                <span className="cube-quota-head">
                  <span className="truncate">
                    {row.quotaLabel}
                    {row.bindingWindow && <span className="cube-muted"> · {row.bindingWindow}</span>}
                  </span>
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

            {rowReason && !isOpen && <div className="cube-row-reason">{rowReason}</div>}

            {isOpen && (
              <RowDetail
                row={row}
                now={now}
                busy={busy}
                liveAuthReady={liveAuthReady}
                onManualBorrow={onManualBorrow}
                onManualReturn={onManualReturn}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}

function RowDetail({
  row,
  now,
  busy,
  liveAuthReady,
  onManualBorrow,
  onManualReturn,
}: {
  row: AccountRow;
  now: number;
  busy: boolean;
  liveAuthReady: boolean;
  onManualBorrow?: (id: string) => void | Promise<void>;
  onManualReturn?: (id: string) => void | Promise<void>;
}) {
  const { t } = useLang();
  return (
    <div className="cube-row-detail">
      <div className="cube-detail-grid">
        <Detail label={t("归属", "owner")} value={row.ownerMode === "client" ? `client ${row.leaseClientId || "-"}` : "cloud"} />
        <Detail label={t("运行状态", "runtime")} value={runtimeChip(row, t).label} />
        <Detail label={t("代次", "gen")} value={String(row.generation)} />
        <Detail label={t("凭据", "auth")} value={row.authPresent ? t("就绪", "ready") : t("缺失", "missing")} />
        <Detail label={t("配额来源", "source")} value={row.quotaSource || "-"} />
        <Detail label={t("5h 重置", "5h reset")} value={row.resetsAt ? `${shortTime(row.resetsAt)} · ${countdown(row.resetsAt, now)}` : "-"} />
        <Detail
          label={t("7d 余量", "7d left")}
          value={
            typeof row.sevenDayPercent === "number"
              ? `${row.sevenDayLabel || `${Math.round(row.sevenDayPercent)}%`}${row.sevenDayResetsAt ? ` · ${countdown(row.sevenDayResetsAt, now)}` : ""}`
              : "-"
          }
        />
        <Detail
          label={t("当前租约", "current lease")}
          value={row.leaseActive ? `${dispatchTarget(row.leaseClientId, "", row.leaseHolder)} · ${t("至", "until")} ${shortTime(row.leaseExpiresAt)}` : "-"}
        />
      </div>
      <ManualLeaseActions
        row={row}
        busy={busy}
        liveAuthReady={liveAuthReady}
        onManualBorrow={onManualBorrow}
        onManualReturn={onManualReturn}
      />
      {(row.runtimeReason || row.reason) && <div className="cube-detail-reason">{row.runtimeReason || row.reason}</div>}
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

function ManualLeaseActions({
  row,
  busy,
  liveAuthReady,
  onManualBorrow,
  onManualReturn,
}: {
  row: AccountRow;
  busy: boolean;
  liveAuthReady: boolean;
  onManualBorrow?: (id: string) => void | Promise<void>;
  onManualReturn?: (id: string) => void | Promise<void>;
}) {
  const { t } = useLang();
  const manual = isManualLease(row);

  if (!row.leaseActive && onManualBorrow) {
    return (
      <div className="cube-detail-actions">
        <Button
          className="gap-1.5"
          isDisabled={busy || !liveAuthReady}
          size="sm"
          variant="primary"
          onPress={() => onManualBorrow(row.id)}
        >
          <KeyRound size={14} />
          {t("手工租用当前 live auth", "Borrow current live auth")}
        </Button>
      </div>
    );
  }

  if (manual && onManualReturn) {
    return (
      <div className="cube-detail-actions">
        <Button className="gap-1.5" isDisabled={busy} size="sm" variant="danger-soft" onPress={() => onManualReturn(row.id)}>
          <Undo2 size={14} />
          {t("归还", "Return")}
        </Button>
      </div>
    );
  }

  return null;
}

function runtimeChip(row: AccountRow, t: TranslateFn): { label: string; color: ChipColor } {
  switch (row.runtimeState) {
    case "available":
      return { label: t("可用", "available"), color: "success" };
    case "leased":
      return { label: t("租用中", "leased"), color: "accent" };
    case "quota_cooldown":
      return { label: t("配额冷却", "quota cooldown"), color: "danger" };
    case "refresh_needed":
      return { label: t("需刷新", "refresh needed"), color: "warning" };
    case "quota_telemetry_missing":
      return { label: t("遥测缺失", "telemetry missing"), color: "warning" };
    case "unavailable":
      return { label: t("不可用", "unavailable"), color: "default" };
    default:
      if (row.eligible) return { label: t("在池中", "in pool"), color: "success" };
      return { label: t("池外", "out"), color: "warning" };
  }
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
