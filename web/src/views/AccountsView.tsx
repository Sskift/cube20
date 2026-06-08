import { Card, Chip } from "@heroui/react";
import { Database, KeyRound, Users } from "lucide-react";

import { useLang } from "../i18n";
import { InventoryTable } from "../components/InventoryTable";
import { accountName } from "../lib/format";
import type { DashboardData } from "../hooks/useDashboardData";

// The accounts page is the INVENTORY view: what accounts exist and how they are
// configured (plan / status / auth+config files / ownership). It deliberately
// carries no dispatch info — "who runs next / alerts / quota pressure" lives on
// the load-balancer page so the two pages never look alike. Per-account editing
// happens in the right-hand details panel when a row is selected.
export function AccountsView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { accounts, readyCount, activeAccount, activeClientCount, selectedId, setSelectedId } = data;

  const authReady = accounts.filter((a) => a.authPresent).length;
  const clientOwned = accounts.filter((a) => a.ownerMode === "client").length;

  const stats = [
    { key: "total", icon: <Database size={15} />, label: t("账号总数", "Total"), value: `${accounts.length}` },
    { key: "ready", icon: <Database size={15} />, label: t("就绪", "Ready"), value: `${readyCount}`, tone: readyCount ? "success" : "danger" },
    { key: "auth", icon: <KeyRound size={15} />, label: t("有凭据", "With auth"), value: `${authReady}/${accounts.length}`, tone: authReady === accounts.length ? "success" : "warning" },
    { key: "owner", icon: <Users size={15} />, label: t("客户端归属", "Client-owned"), value: `${clientOwned}` },
  ] as const;

  return (
    <section className="cube-view-panel flex flex-col gap-4">
      <div className="cube-inv-stats">
        {stats.map((s) => (
          <div key={s.key} className="cube-inv-stat">
            <span className="cube-inv-stat-icon">{s.icon}</span>
            <span className="min-w-0">
              <span className="cube-inv-stat-label">{s.label}</span>
              <span className={`cube-inv-stat-value ${"tone" in s && s.tone ? `tone-${s.tone}` : ""}`}>{s.value}</span>
            </span>
          </div>
        ))}
      </div>

      <Card className="cube-card">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-slate-950">{t("账号清单", "Account inventory")}</h2>
            <p className="path-text text-xs text-slate-500">
              {t("活跃", "Active")}: {accountName(activeAccount)} · {activeClientCount} {t("个在线客户端", "clients online")}
            </p>
          </div>
          <Chip color="success" size="sm" variant="soft">
            {readyCount} {t("就绪", "ready")}
          </Chip>
        </Card.Header>
        <Card.Content className="p-3 sm:p-4">
          {accounts.length ? (
            <InventoryTable accounts={accounts} selectedId={selectedId} onSelect={(id) => setSelectedId(id)} />
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
