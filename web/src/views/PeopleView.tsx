import { useEffect, useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { KeyRound, MonitorSmartphone, Users } from "lucide-react";

import { useLang } from "../i18n";
import { cloudOrigin } from "../api";
import { CopyLine, FieldLabel } from "../components/primitives";
import { shortTime } from "../lib/format";
import type { DashboardData } from "../hooks/useDashboardData";

// The people page is now a USERS roster: website user accounts (username,
// status, device count). The legacy PAT-mint flow is kept as a de-emphasized
// fallback at the bottom for headless/client bootstrap.
export function PeopleView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { users, busy, createdClientToken } = data;
  const [clientLabel, setClientLabel] = useState("");

  useEffect(() => {
    void data.loadUsers().catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const activeUsers = users.filter((u) => !u.disabled).length;

  return (
    <section className="cube-view-panel flex flex-col gap-4">
      <Card className="cube-card">
        <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <Users size={17} />
            {t("用户", "Users")}
          </h2>
          <Chip color={activeUsers > 0 ? "success" : "warning"} variant="soft">
            {activeUsers}/{users.length}
          </Chip>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-sm">
              <thead>
                <tr className="border-b border-slate-200 text-left text-xs font-medium uppercase text-slate-500">
                  <th className="px-4 py-2.5">{t("用户名", "Username")}</th>
                  <th className="px-4 py-2.5">{t("状态", "Status")}</th>
                  <th className="px-4 py-2.5">{t("设备数", "Devices")}</th>
                  <th className="px-4 py-2.5">{t("最近登录", "Last login")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {users.map((user) => (
                  <tr key={user.id} className="text-slate-800">
                    <td className="px-4 py-2.5">
                      <div className="truncate font-semibold text-slate-950">{user.username}</div>
                      <div className="font-mono text-[11px] text-slate-400">{user.id}</div>
                    </td>
                    <td className="px-4 py-2.5">
                      <Chip color={user.disabled ? "danger" : "success"} size="sm" variant="soft">
                        {user.disabled ? t("已禁用", "disabled") : t("活跃", "active")}
                      </Chip>
                    </td>
                    <td className="px-4 py-2.5">
                      <span className="inline-flex items-center gap-1.5 text-slate-600">
                        <MonitorSmartphone size={14} />
                        {user.deviceCount}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-slate-500">{shortTime(user.lastLoginAt)}</td>
                  </tr>
                ))}
                {!users.length && (
                  <tr>
                    <td className="px-4 py-6 text-sm text-slate-500" colSpan={4}>
                      {t("暂无用户", "No users")}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </Card.Content>
      </Card>

      {/* De-emphasized legacy PAT mint, for headless/client bootstrap. */}
      <Card className="cube-card">
        <Card.Header className="border-b border-slate-200 px-5 py-4">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-slate-700">
            <KeyRound size={15} />
            {t("旧版 PAT(备用)", "Legacy PAT (fallback)")}
          </h2>
        </Card.Header>
        <Card.Content className="gap-4">
          <FieldLabel text={t("客户端标签", "client label")}>
            <Input
              fullWidth
              placeholder="liushiao-local"
              value={clientLabel}
              variant="secondary"
              onChange={(event) => setClientLabel(event.currentTarget.value)}
            />
          </FieldLabel>
          <Button
            className="gap-2"
            isDisabled={busy || !clientLabel.trim()}
            variant="secondary"
            onPress={async () => {
              await data.createClient(clientLabel.trim());
              setClientLabel("");
            }}
          >
            <KeyRound size={15} />
            {t("创建 PAT", "Create PAT")}
          </Button>
          {createdClientToken && (
            <div className="rounded-lg border border-success bg-success-soft p-3">
              <div className="mb-2 text-xs font-semibold uppercase text-success-soft-foreground">{t("令牌", "Token")}</div>
              <div className="path-text font-mono text-xs text-slate-950">{createdClientToken}</div>
              <div className="mt-3 grid grid-cols-1 gap-2">
                <CopyLine label={t("仪表盘", "Dashboard")} value={`${cloudOrigin()}/?token=${createdClientToken}`} />
                <CopyLine
                  label={t("本地配置", "Local config")}
                  value={`cube cloud config --server ${cloudOrigin()} --token ${createdClientToken}`}
                />
              </div>
            </div>
          )}
        </Card.Content>
      </Card>
    </section>
  );
}
