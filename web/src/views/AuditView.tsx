import { useEffect, useMemo, useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { Filter, RefreshCw, ScrollText } from "lucide-react";

import { useLang } from "../i18n";
import { FieldLabel } from "../components/primitives";
import { dispatchEventLabel, shortID, shortTime } from "../lib/format";
import type { DashboardData } from "../hooks/useDashboardData";

// AuditView is the admin-gated, uncapped dispatch-event table: user × device ×
// account × event × time, with filter inputs and before/limit "load more". The
// LoadBalancerView keeps its small recent preview; this is the full ledger.
export function AuditView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { dispatches, busy } = data;
  const [user, setUser] = useState("");
  const [account, setAccount] = useState("");
  const [device, setDevice] = useState("");
  const [event, setEvent] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);

  const limit = 200;

  const applyFilters = async () => {
    await data.fetchDispatches({
      limit,
      user: user.trim() || undefined,
      account: account.trim() || undefined,
      device: device.trim() || undefined,
      event: event.trim() || undefined,
    });
  };

  // Load the uncapped slice on mount.
  useEffect(() => {
    void data.fetchDispatches({ limit }).catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Oldest event currently loaded — the cursor for "load more".
  const oldest = useMemo(() => {
    if (!dispatches.length) return undefined;
    return dispatches[dispatches.length - 1]?.createdAt;
  }, [dispatches]);

  const loadMore = async () => {
    if (!oldest) return;
    setLoadingMore(true);
    try {
      const older = await data.fetchDispatchPage({
        limit,
        before: oldest,
        user: user.trim() || undefined,
        account: account.trim() || undefined,
        device: device.trim() || undefined,
        event: event.trim() || undefined,
      });
      data.appendDispatches(older);
    } catch {
      // surfaced via the shared message banner
    } finally {
      setLoadingMore(false);
    }
  };

  return (
    <section className="cube-view-panel flex flex-col gap-4">
      <Card className="cube-card">
        <Card.Header className="flex items-center gap-2 border-b border-slate-200 px-5 py-4">
          <Filter size={16} />
          <h2 className="text-sm font-semibold text-slate-950">{t("筛选", "Filters")}</h2>
        </Card.Header>
        <Card.Content className="gap-3">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <FieldLabel text={t("用户名", "username")}>
              <Input fullWidth value={user} variant="secondary" onChange={(e) => setUser(e.currentTarget.value)} />
            </FieldLabel>
            <FieldLabel text={t("账号", "account")}>
              <Input fullWidth value={account} variant="secondary" onChange={(e) => setAccount(e.currentTarget.value)} />
            </FieldLabel>
            <FieldLabel text={t("设备", "device")}>
              <Input fullWidth value={device} variant="secondary" onChange={(e) => setDevice(e.currentTarget.value)} />
            </FieldLabel>
            <FieldLabel text={t("事件类型", "event type")}>
              <Input fullWidth placeholder="claimed / released / expired" value={event} variant="secondary" onChange={(e) => setEvent(e.currentTarget.value)} />
            </FieldLabel>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button className="gap-2" isDisabled={busy} variant="primary" onPress={applyFilters}>
              <Filter size={15} />
              {t("应用筛选", "Apply filters")}
            </Button>
            <Button
              className="gap-2"
              isDisabled={busy}
              variant="secondary"
              onPress={async () => {
                setUser("");
                setAccount("");
                setDevice("");
                setEvent("");
                await data.fetchDispatches({ limit });
              }}
            >
              <RefreshCw size={15} />
              {t("重置", "Reset")}
            </Button>
          </div>
        </Card.Content>
      </Card>

      <Card className="cube-card">
        <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <ScrollText size={17} />
            {t("调度审计", "Dispatch audit")}
          </h2>
          <Chip color={dispatches.length ? "success" : "warning"} variant="soft">
            {dispatches.length}
          </Chip>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 text-left text-xs font-medium uppercase text-slate-500">
                  <th className="px-4 py-2.5">{t("用户", "User")}</th>
                  <th className="px-4 py-2.5">{t("设备", "Device")}</th>
                  <th className="px-4 py-2.5">{t("账号", "Account")}</th>
                  <th className="px-4 py-2.5">{t("事件", "Event")}</th>
                  <th className="px-4 py-2.5">{t("时间", "Time")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {dispatches.map((d) => (
                  <tr key={d.id} className="text-slate-800">
                    <td className="px-4 py-2.5">
                      <span className="truncate font-medium text-slate-900">
                        {d.username ?? d.clientLabel ?? (d.clientId ? shortID(d.clientId) : "-")}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-slate-600">{d.deviceLabel ?? d.holder ?? "-"}</td>
                    <td className="px-4 py-2.5 text-slate-600">{d.accountLabel || shortID(d.accountId)}</td>
                    <td className="px-4 py-2.5">
                      <Chip
                        color={d.event === "claimed" ? "success" : d.event === "expired" ? "danger" : "default"}
                        size="sm"
                        variant="soft"
                      >
                        {dispatchEventLabel(d.event, t)}
                      </Chip>
                    </td>
                    <td className="px-4 py-2.5 text-slate-500">{shortTime(d.createdAt)}</td>
                  </tr>
                ))}
                {!dispatches.length && (
                  <tr>
                    <td className="px-4 py-6 text-sm text-slate-500" colSpan={5}>
                      {t("没有匹配的调度记录。", "No matching dispatch events.")}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          {dispatches.length > 0 && (
            <div className="flex justify-center border-t border-slate-100 px-4 py-3">
              <Button
                className="gap-2"
                isDisabled={busy || loadingMore || !oldest}
                size="sm"
                variant="secondary"
                onPress={loadMore}
              >
                <RefreshCw size={14} />
                {loadingMore ? t("加载中…", "Loading…") : t("加载更多", "Load more")}
              </Button>
            </div>
          )}
        </Card.Content>
      </Card>
    </section>
  );
}
