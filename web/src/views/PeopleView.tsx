import { useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { KeyRound, Trash2, Users } from "lucide-react";

import { useLang } from "../i18n";
import { cloudOrigin } from "../api";
import { CopyLine, FieldLabel } from "../components/primitives";
import { shortTime } from "../lib/format";
import type { DashboardData } from "../hooks/useDashboardData";

// The people page: mint personal access tokens (PATs) for clients and review /
// revoke the existing roster. Workspace membership is managed on the Workspaces
// page; this page is purely about identities (PATs).
export function PeopleView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { clients, activeClientCount, busy, createdClientToken } = data;
  const [clientLabel, setClientLabel] = useState("");

  return (
    <section className="cube-view-panel">
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
        <Card className="cube-card">
          <Card.Header className="border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <KeyRound size={17} />
              {t("新建 PAT", "New PAT")}
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
              isDisabled={busy}
              variant="primary"
              onPress={async () => {
                await data.createClient(clientLabel);
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
                  <CopyLine
                    label={t("仪表盘", "Dashboard")}
                    value={`${cloudOrigin()}/?token=${createdClientToken}`}
                  />
                  <CopyLine
                    label={t("本地配置", "Local config")}
                    value={`cube cloud config --server ${cloudOrigin()} --token ${createdClientToken}`}
                  />
                </div>
              </div>
            )}
          </Card.Content>
        </Card>

        <Card className="cube-card">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Users size={17} />
              {t("成员", "People")}
            </h2>
            <Chip color={activeClientCount > 0 ? "success" : "warning"} variant="soft">
              {activeClientCount}/{clients.length}
            </Chip>
          </Card.Header>
          <Card.Content className="p-0">
            <div className="divide-y divide-slate-200">
              {clients.map((client) => (
                <div key={client.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate text-sm font-semibold text-slate-950">{client.label || client.id}</span>
                      <Chip color={client.active ? "success" : "danger"} size="sm" variant="soft">
                        {client.active ? t("活跃", "active") : t("已吊销", "revoked")}
                      </Chip>
                    </div>
                    <div className="mt-1 font-mono text-xs text-slate-500">{client.id}</div>
                    <div className="mt-1 text-xs text-slate-500">{t("最近活跃", "last seen")} {shortTime(client.lastSeenAt)}</div>
                  </div>
                  <Button
                    aria-label={`Revoke ${client.id}`}
                    className="gap-2"
                    isDisabled={busy || !client.active}
                    size="sm"
                    variant="danger-soft"
                    onPress={() => data.revokeClient(client.id)}
                  >
                    <Trash2 size={14} />
                    {t("吊销", "Revoke")}
                  </Button>
                </div>
              ))}
              {!clients.length && <div className="px-4 py-6 text-sm text-slate-500">{t("暂无客户端", "No clients")}</div>}
            </div>
          </Card.Content>
        </Card>
      </div>
    </section>
  );
}
