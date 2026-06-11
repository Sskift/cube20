import { useEffect, useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { FolderPlus, KeyRound, Trash2, UserPlus, Users } from "lucide-react";

import { useLang } from "../i18n";
import { cloudOrigin } from "../api";
import { CopyLine, FieldLabel } from "../components/primitives";
import { shortTime } from "../lib/format";
import type { Membership, WorkspaceRole } from "../types";
import type { DashboardData } from "../hooks/useDashboardData";

// The people page: mint PATs, manage the client roster, and manage workspaces
// (pools) + their members. Owns local form state only; every record and mutation
// comes from the shared dashboard data hook.
export function PeopleView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { clients, activeClientCount, busy, createdClientToken, workspaces } = data;
  const [clientLabel, setClientLabel] = useState("");
  const [workspaceName, setWorkspaceName] = useState("");

  return (
    <section className="cube-view-panel flex flex-col gap-4">
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

      <Card className="cube-card">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <FolderPlus size={17} />
            {t("工作区", "Workspaces")}
          </h2>
          <div className="flex shrink-0 items-center gap-2">
            <Input
              className="w-44"
              placeholder={t("新工作区名称", "new workspace name")}
              value={workspaceName}
              variant="secondary"
              onChange={(event) => setWorkspaceName(event.currentTarget.value)}
            />
            <Button
              className="gap-2"
              isDisabled={busy || !workspaceName.trim()}
              variant="primary"
              onPress={async () => {
                await data.createWorkspace(workspaceName.trim());
                setWorkspaceName("");
              }}
            >
              <FolderPlus size={15} />
              {t("创建", "Create")}
            </Button>
          </div>
        </Card.Header>
        <Card.Content className="gap-3">
          {workspaces.length ? (
            workspaces.map((ws) => <WorkspaceMembersCard key={ws.id} data={data} workspaceId={ws.id} name={ws.name || ws.id} />)
          ) : (
            <div className="px-1 py-4 text-sm text-slate-500">{t("暂无工作区", "No workspaces yet")}</div>
          )}
        </Card.Content>
      </Card>
    </section>
  );
}

// WorkspaceMembersCard lists a single workspace's members and lets an admin
// invite/remove them and toggle their role. Members are loaded lazily per card.
function WorkspaceMembersCard({ data, workspaceId, name }: { data: DashboardData; workspaceId: string; name: string }) {
  const { t } = useLang();
  const { clients, busy } = data;
  const [members, setMembers] = useState<Membership[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [inviteId, setInviteId] = useState("");
  const [inviteRole, setInviteRole] = useState<WorkspaceRole>("member");

  const refresh = async () => {
    try {
      const list = await data.listMembers(workspaceId);
      setMembers(list);
      setLoaded(true);
    } catch {
      setLoaded(true);
    }
  };

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaceId]);

  const memberIds = new Set(members.map((m) => m.clientId));
  const candidates = clients.filter((c) => c.active && !memberIds.has(c.id));
  const labelFor = (id: string) => clients.find((c) => c.id === id)?.label || id;

  return (
    <div className="rounded-lg border border-slate-200">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-4 py-3">
        <div className="min-w-0">
          <div className="text-sm font-semibold text-slate-950">{name}</div>
          <div className="font-mono text-xs text-slate-400">{workspaceId}</div>
        </div>
        <Chip color="accent" size="sm" variant="soft">
          {members.length} {t("成员", "members")}
        </Chip>
      </div>

      <div className="divide-y divide-slate-100">
        {members.map((m) => (
          <div key={m.clientId} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5">
            <div className="min-w-0">
              <span className="truncate text-sm text-slate-900">{labelFor(m.clientId)}</span>
              <div className="font-mono text-[11px] text-slate-400">{m.clientId}</div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <select
                aria-label={t("角色", "role")}
                className="h-8 rounded-md border border-slate-200 bg-surface px-2 text-xs text-slate-700 outline-none focus:border-slate-400"
                value={m.role}
                disabled={busy}
                onChange={async (event) => {
                  await data.setMember(workspaceId, m.clientId, event.currentTarget.value as WorkspaceRole);
                  await refresh();
                }}
              >
                <option value="member">{t("成员", "member")}</option>
                <option value="admin">{t("管理员", "admin")}</option>
              </select>
              <Button
                aria-label={`Remove ${m.clientId}`}
                isDisabled={busy}
                size="sm"
                variant="danger-soft"
                onPress={async () => {
                  await data.removeMember(workspaceId, m.clientId);
                  await refresh();
                }}
              >
                <Trash2 size={13} />
              </Button>
            </div>
          </div>
        ))}
        {loaded && !members.length && <div className="px-4 py-3 text-xs text-slate-500">{t("暂无成员", "No members")}</div>}
      </div>

      <div className="flex flex-wrap items-center gap-2 border-t border-slate-100 px-4 py-3">
        <select
          aria-label={t("选择客户端", "select client")}
          className="h-8 min-w-[10rem] rounded-md border border-slate-200 bg-surface px-2 text-xs text-slate-700 outline-none focus:border-slate-400"
          value={inviteId}
          onChange={(event) => setInviteId(event.currentTarget.value)}
        >
          <option value="">{t("选择客户端…", "select client…")}</option>
          {candidates.map((c) => (
            <option key={c.id} value={c.id}>
              {c.label || c.id}
            </option>
          ))}
        </select>
        <select
          aria-label={t("角色", "role")}
          className="h-8 rounded-md border border-slate-200 bg-surface px-2 text-xs text-slate-700 outline-none focus:border-slate-400"
          value={inviteRole}
          onChange={(event) => setInviteRole(event.currentTarget.value as WorkspaceRole)}
        >
          <option value="member">{t("成员", "member")}</option>
          <option value="admin">{t("管理员", "admin")}</option>
        </select>
        <Button
          className="gap-1.5"
          isDisabled={busy || !inviteId}
          size="sm"
          variant="secondary"
          onPress={async () => {
            await data.setMember(workspaceId, inviteId, inviteRole);
            setInviteId("");
            setInviteRole("member");
            await refresh();
          }}
        >
          <UserPlus size={14} />
          {t("添加", "Add")}
        </Button>
      </div>
    </div>
  );
}
