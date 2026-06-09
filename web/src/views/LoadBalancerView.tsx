import { Card, Chip } from "@heroui/react";
import { Route } from "lucide-react";

import { useLang } from "../i18n";
import { useNow } from "../hooks/useNow";
import { AccountTable } from "../components/AccountTable";
import { DispatchTimeline } from "../components/DispatchTimeline";
import { AlertsPanel, NextAccountCard, OverviewBar } from "../components/Overview";
import { QuotaRing } from "../components/primitives";
import { lbAccountName, scoreLabel, shortTime } from "../lib/format";
import { buildAlerts, buildLbRows, quotaPressure, type OverviewItem, type Tone } from "../lib/rows";
import type { DashboardData } from "../hooks/useDashboardData";

// The load-balancer page, rebuilt as a single information architecture: a top
// overview strip, the next-dispatch candidate, an alerts column, then ONE
// scannable account table (health / quota / reset / lease / reason / score) with
// click-to-expand detail. This replaces the old triple visualization
// (LoadBalanceAccountCard cards + RoutingMap rows + RefreshQueueBar queue).
export function LoadBalancerView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const now = useNow();
  const { lb, eligibleCount, lbTotalCount, latestDispatchByAccount, dispatches } = data;

  const rows = buildLbRows(lb, latestDispatchByAccount, t);
  const alerts = buildAlerts(rows, t);
  const pressured = quotaPressure(rows, now);
  const next = lb?.eligible[0];

  const poolTone: Tone = eligibleCount === 0 ? "danger" : eligibleCount < lbTotalCount ? "warning" : "success";
  const items: OverviewItem[] = [
    {
      key: "pool",
      label: t("就绪池", "Ready pool"),
      value: `${eligibleCount}/${lbTotalCount || 0}`,
      sub: lb?.policy || "quota-aware",
      tone: poolTone,
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
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Route size={17} />
              {t("下一个租约候选", "Next lease candidate")}
            </h2>
          </Card.Header>
          <Card.Content>
            <NextAccountCard
              empty={!next}
              emptyHint={t("当前没有账号同时具备就绪凭据、无活跃租约、且有可用 5h 配额。", "No account currently has ready auth, no active lease, and available 5h quota.")}
              name={next ? lbAccountName(next) : ""}
              detail={
                next
                  ? typeof next.quotaSevenDayRemainingPercent === "number"
                    ? `${next.id} · 7d ${next.quotaSevenDayRemainingDisplay || `${Math.round(next.quotaSevenDayRemainingPercent)}%`}`
                    : next.id
                  : undefined
              }
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
            <AlertsPanel alerts={alerts} emptyLabel={t("所有云端账号当前都可分配。", "Every cloud-owned account is currently assignable.")} />
          </Card.Content>
        </Card>
      </div>

      <Card className="cube-card">
        <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h2 className="text-base font-semibold text-slate-950">{t("账号池", "Account pool")}</h2>
          <Chip color={eligibleCount ? "success" : "danger"} variant="soft">
            {eligibleCount}/{lbTotalCount || 0} {t("可分配", "assignable")}
          </Chip>
        </Card.Header>
        <Card.Content className="p-3 sm:p-4">
          <AccountTable rows={rows} showScore showPool />
        </Card.Content>
      </Card>

      <Card className="cube-card">
        <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h2 className="text-base font-semibold text-slate-950">{t("调度历史", "Dispatch history")}</h2>
          <Chip color={dispatches.length ? "success" : "warning"} variant="soft">
            {dispatches.length}
          </Chip>
        </Card.Header>
        <Card.Content className="gap-2">
          <DispatchTimeline dispatches={dispatches.slice(0, 10)} />
        </Card.Content>
      </Card>
    </section>
  );
}
