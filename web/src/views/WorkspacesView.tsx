import { useEffect, useMemo, useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { Database, FolderPlus, Layers, Trash2, UserPlus, Users } from "lucide-react";

import { useLang } from "../i18n";
import { EmptyState } from "../components/primitives";
import type { Membership, Workspace, WorkspaceRole } from "../types";
import type { DashboardData } from "../hooks/useDashboardData";

// The Workspaces page: a master/detail layout (left = pool list, right = the
// selected pool's accounts + members), modeled on the X workspace settings page.
// Admins create pools, move accounts between them, and manage member roles.
export function WorkspacesView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { workspaces, accounts, busy } = data;
  const [activeId, setActiveId] = useState("");
  const [newName, setNewName] = useState("");

  // Default the selection to the first workspace once data arrives.
  useEffect(() => {
    if (!activeId && workspaces.length) setActiveId(workspaces[0].id);
  }, [workspaces, activeId]);

  const active = workspaces.find((ws) => ws.id === activeId);
  const accountCountByWs = useMemo(() => {
    const map = new Map<string, number>();
    for (const a of accounts) {
      const id = a.workspaceId || "default";
      map.set(id, (map.get(id) || 0) + 1);
    }
    return map;
  }, [accounts]);

  return (
    <section className="cube-view-panel grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,18rem)_minmax(0,1fr)]">
      {/* Left: workspace list + create */}
      <Card className="cube-card h-max">
        <Card.Header className="flex items-center justify-between gap-2 border-b border-slate-200 px-4 py-3">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-slate-950">
            <Layers size={16} />
            {t("工作区", "Workspaces")}
          </h2>
          <Chip color="accent" size="sm" variant="soft">
            {workspaces.length}
          </Chip>
        </Card.Header>
        <Card.Content className="p-2">
          <div className="flex flex-col gap-1">
            {workspaces.map((ws) => {
              const isActive = ws.id === activeId;
              return (
                <button
                  key={ws.id}
                  type="button"
                  className={`flex items-center justify-between gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors ${
                    isActive ? "cube-brand shadow-sm" : "text-slate-700 hover:bg-slate-100"
                  }`}
                  onClick={() => setActiveId(ws.id)}
                >
                  <span className="min-w-0 truncate font-medium">{ws.name || ws.id}</span>
                  <span className={`shrink-0 rounded-full px-1.5 text-[10px] ${isActive ? "bg-white/15 text-white" : "bg-slate-200 text-slate-600"}`}>
                    {accountCountByWs.get(ws.id) || 0}
                  </span>
                </button>
              );
            })}
            {!workspaces.length && <div className="px-3 py-6 text-xs text-slate-500">{t("暂无工作区", "No workspaces")}</div>}
          </div>
          <div className="mt-2 flex items-center gap-2 border-t border-slate-100 pt-2">
            <Input
              className="flex-1"
              placeholder={t("新工作区", "new pool")}
              value={newName}
              variant="secondary"
              onChange={(event) => setNewName(event.currentTarget.value)}
            />
            <Button
              isDisabled={busy || !newName.trim()}
              size="sm"
              variant="primary"
              onPress={async () => {
                await data.createWorkspace(newName.trim());
                setNewName("");
              }}
            >
              <FolderPlus size={15} />
            </Button>
          </div>
        </Card.Content>
      </Card>

      {/* Right: selected workspace detail */}
      {active ? (
        <WorkspaceDetail key={active.id} data={data} workspace={active} accountCount={accountCountByWs.get(active.id) || 0} />
      ) : (
        <Card className="cube-card">
          <Card.Content>
            <EmptyState className="py-16">
              <EmptyState.Media>
                <Layers size={24} />
              </EmptyState.Media>
              <EmptyState.Title>{t("选择一个工作区", "Select a workspace")}</EmptyState.Title>
              <EmptyState.Description>{t("在左侧选择或创建一个账号池来管理它的账号与成员。", "Pick or create a pool on the left to manage its accounts and members.")}</EmptyState.Description>
            </EmptyState>
          </Card.Content>
        </Card>
      )}
    </section>
  );
}

function WorkspaceDetail({ data, workspace, accountCount }: { data: DashboardData; workspace: Workspace; accountCount: number }) {
  const { t } = useLang();
  const { accounts, clients, busy } = data;
  const [members, setMembers] = useState<Membership[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [inviteId, setInviteId] = useState("");
  const [inviteRole, setInviteRole] = useState<WorkspaceRole>("member");

  const refresh = async () => {
    try {
      setMembers(await data.listMembers(workspace.id));
    } catch {
      // surfaced via the shared message banner
    } finally {
      setLoaded(true);
    }
  };

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace.id]);

  const poolAccounts = accounts.filter((a) => (a.workspaceId || "default") === workspace.id);
  const memberIds = new Set(members.map((m) => m.clientId));
  const candidates = clients.filter((c) => c.active && !memberIds.has(c.id));
  const labelFor = (id: string) => clients.find((c) => c.id === id)?.label || id;
  // A member is keyed by clientId today, but the user/device feature adds an
  // optional userId. Prefer a username-style display (userId) when present and
  // fall back to the client label so legacy client-only members keep working.
  const memberKey = (m: Membership) => m.userId || m.clientId;
  const memberDisplay = (m: Membership) => m.userId || labelFor(m.clientId);

  return (
    <div className="flex flex-col gap-4">
      <Card className="cube-card">
        <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-slate-950">{workspace.name || workspace.id}</h2>
            <div className="mt-0.5 font-mono text-xs text-slate-400">{workspace.id}</div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Chip color="default" size="sm" variant="soft">
              {accountCount} {t("账号", "accounts")}
            </Chip>
            <Chip color="accent" size="sm" variant="soft">
              {members.length} {t("成员", "members")}
            </Chip>
          </div>
        </Card.Header>
      </Card>

      {/* Accounts in this pool */}
      <Card className="cube-card">
        <Card.Header className="flex items-center gap-2 border-b border-slate-200 px-5 py-4">
          <Database size={16} />
          <h3 className="text-sm font-semibold text-slate-950">{t("池内账号", "Pool accounts")}</h3>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="divide-y divide-slate-100">
            {poolAccounts.map((a) => (
              <div key={a.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium text-slate-900">{a.label || a.id}</span>
                    <Chip color={a.status === "ready" ? "success" : "warning"} size="sm" variant="soft">
                      {a.status}
                    </Chip>
                  </div>
                  <div className="font-mono text-[11px] text-slate-400">{a.id}</div>
                </div>
                <select
                  aria-label={t("移动到工作区", "move to workspace")}
                  className="h-8 max-w-[10rem] rounded-md border border-slate-200 bg-surface px-2 text-xs text-slate-700 outline-none focus:border-slate-400"
                  value={workspace.id}
                  disabled={busy}
                  onChange={async (event) => {
                    const target = event.currentTarget.value;
                    if (target !== workspace.id) await data.setAccountWorkspace(a.id, target);
                  }}
                >
                  {data.workspaces.map((ws) => (
                    <option key={ws.id} value={ws.id}>
                      {ws.name || ws.id}
                    </option>
                  ))}
                </select>
              </div>
            ))}
            {!poolAccounts.length && <div className="px-4 py-5 text-xs text-slate-500">{t("此池暂无账号", "No accounts in this pool")}</div>}
          </div>
        </Card.Content>
      </Card>

      {/* Members */}
      <Card className="cube-card">
        <Card.Header className="flex items-center gap-2 border-b border-slate-200 px-5 py-4">
          <Users size={16} />
          <h3 className="text-sm font-semibold text-slate-950">{t("成员", "Members")}</h3>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="divide-y divide-slate-100">
            {members.map((m) => (
              <div key={memberKey(m)} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5">
                <div className="min-w-0">
                  <span className="truncate text-sm text-slate-900">{memberDisplay(m)}</span>
                  <div className="font-mono text-[11px] text-slate-400">{m.clientId}</div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <select
                    aria-label={t("角色", "role")}
                    className="h-8 rounded-md border border-slate-200 bg-surface px-2 text-xs text-slate-700 outline-none focus:border-slate-400"
                    value={m.role}
                    disabled={busy}
                    onChange={async (event) => {
                      await data.setMember(workspace.id, m.clientId, event.currentTarget.value as WorkspaceRole);
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
                      await data.removeMember(workspace.id, m.clientId);
                      await refresh();
                    }}
                  >
                    <Trash2 size={13} />
                  </Button>
                </div>
              </div>
            ))}
            {loaded && !members.length && <div className="px-4 py-5 text-xs text-slate-500">{t("暂无成员", "No members")}</div>}
          </div>
          {/* Add member */}
          <div className="flex flex-wrap items-center gap-2 border-t border-slate-100 px-4 py-3">
            <select
              aria-label={t("选择客户端", "select client")}
              className="h-8 min-w-[11rem] rounded-md border border-slate-200 bg-surface px-2 text-xs text-slate-700 outline-none focus:border-slate-400"
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
                await data.setMember(workspace.id, inviteId, inviteRole);
                setInviteId("");
                setInviteRole("member");
                await refresh();
              }}
            >
              <UserPlus size={14} />
              {t("添加", "Add")}
            </Button>
          </div>
        </Card.Content>
      </Card>
    </div>
  );
}
