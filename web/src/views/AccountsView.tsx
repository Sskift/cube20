import { Card, Chip } from "@heroui/react";
import { Database } from "lucide-react";

import { useLang } from "../i18n";
import { useNow } from "../hooks/useNow";
import { AccountTable } from "../components/AccountTable";
import { AlertsPanel, NextAccountCard, OverviewBar } from "../components/Overview";
import { QuotaRing } from "../components/primitives";
import { accountName, lbAccountName, scoreLabel, shortTime } from "../lib/format";
import {
  buildAccountRows,
  buildAlerts,
  buildLbRows,
  quotaPressure,
  type OverviewItem,
} from "../lib/rows";
import type { DashboardData } from "../hooks/useDashboardData";

// The accounts inventory + first screen. Per the redesign the first screen shows
// only the key info — status overview, next account, alerts, quota pressure —
// followed by the single scannable account table. Per-account editing lives in
// the right-hand details panel (App shell) when a row is selected.
export function AccountsView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const now = useNow();
  const {
    accounts,
    quotas,
    lb,
    refreshByAccount,
    latestDispatchByAccount,
    readyCount,
    eligibleCount,
    lbTotalCount,
    activeAccount,
    selectedId,
    setSelectedId,
  } = data;

  const rows = buildAccountRows(accounts, quotas, refreshByAccount, latestDispatchByAccount, t);
  // Alerts/next/pressure use the LB view of the world (it carries eligibility +
  // score + reason), falling back gracefully when the LB status is still loading.
  const lbRows = buildLbRows(lb, latestDispatchByAccount, t);
  const alerts = buildAlerts(lbRows.length ? lbRows : rows, t);
  const pressured = quotaPressure(lbRows.length ? lbRows : rows, now);
  const next = lb?.eligible[0];

  const items: OverviewItem[] = [
    {
      key: "ready",
      label: t("就绪池", "Ready pool"),
      value: `${readyCount}/${accounts.length}`,
      sub: `${eligibleCount}/${lbTotalCount || 0} ${t("可分配", "assignable")}`,
      tone: readyCount > 0 ? "success" : "danger",
    },
    {
      key: "next",
      label: t("下一账号", "Next account"),
      value: next ? lbAccountName(next) : t("无", "none"),
      sub: next ? `${t("分数", "score")} ${scoreLabel(next.quotaScore)}` : t("无可分配", "none assignable"),
      tone: next ? "accent" : "danger",
    },
    {
      key: "alerts",
      label: t("告警", "Alerts"),
      value: String(alerts.length),
      sub: alerts.length ? t("查看下方", "see below") : t("全部正常", "all clear"),
      tone: alerts.length ? "warning" : "success",
    },
    {
      key: "pressure",
      label: t("配额压力", "Quota pressure"),
      value: String(pressured.length),
      sub: pressured.length ? t("低余量/将重置", "low / near reset") : t("无压力", "none"),
      tone: pressured.length ? "warning" : "success",
    },
  ];

  return (
    <section className="cube-view-panel flex flex-col gap-4">
      <OverviewBar items={items} />

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
        <Card className="cube-card">
          <Card.Header className="border-b border-slate-200 px-5 py-4">
            <h2 className="text-base font-semibold text-slate-950">{t("下一个分配账号", "Next account up")}</h2>
          </Card.Header>
          <Card.Content>
            <NextAccountCard
              empty={!next}
              emptyHint={t("当前没有可分配账号。", "No assignable account right now.")}
              name={next ? lbAccountName(next) : ""}
              detail={next ? next.id : undefined}
              score={next ? scoreLabel(next.quotaScore) : undefined}
              reset={next ? shortTime(next.quotaResetsAt) : undefined}
              scoreLabel={t("分数", "score")}
              resetLabel={t("重置", "reset")}
              ring={next ? <QuotaRing value={next.quotaRemainingPercent} label={next.quotaRemainingDisplay} /> : undefined}
            />
          </Card.Content>
        </Card>

        <Card className="cube-card">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="text-base font-semibold text-slate-950">{t("告警", "Alerts")}</h2>
            <Chip color={alerts.length ? "warning" : "success"} variant="soft">
              {alerts.length}
            </Chip>
          </Card.Header>
          <Card.Content>
            <AlertsPanel alerts={alerts} emptyLabel={t("所有账号状态正常。", "All accounts healthy.")} />
          </Card.Content>
        </Card>
      </div>

      <Card className="cube-card">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-slate-950">{t("账号", "Accounts")}</h2>
            <p className="path-text text-xs text-slate-500">
              {accounts.length} {t("个配置", "profiles")} · {t("活跃", "Active")}: {accountName(activeAccount)}
            </p>
          </div>
          <Chip color="success" size="sm" variant="soft">
            {readyCount} {t("就绪", "ready")}
          </Chip>
        </Card.Header>
        <Card.Content className="p-3 sm:p-4">
          {accounts.length ? (
            <AccountTable
              rows={rows}
              selectedId={selectedId}
              onSelect={(id) => setSelectedId(id)}
            />
          ) : (
            <div className="cube-table-empty">
              <div className="mb-2 flex justify-center text-slate-400">
                <Database size={24} />
              </div>
              <div className="text-sm font-semibold text-slate-950">{t("还没有账号", "No accounts yet")}</div>
              <div className="mt-1 text-xs text-slate-500">{t("导入当前 Codex 配置,或上传一个 auth.json 快照。", "Import your current Codex profile or upload an auth.json snapshot.")}</div>
            </div>
          )}
        </Card.Content>
      </Card>
    </section>
  );
}
