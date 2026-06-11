import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { apiJSON, saveCloudToken } from "../api";
import { accountName } from "../lib/format";
import type {
  AccessMode,
  Account,
  AccountOwnerMode,
  AccountStatus,
  AccountUsage,
  Client,
  DispatchEvent,
  LoadBalanceStatus,
  Membership,
  Meta,
  PersonalPayload,
  QuotaResult,
  RefreshQueueItem,
  TranslateFn,
  Workspace,
  WorkspaceRole,
} from "../types";

export interface AccountDraft {
  label: string;
  status: AccountStatus;
  ownerMode: AccountOwnerMode;
  ownerClientId?: string;
}

// useDashboardData owns every piece of server state the admin dashboard renders,
// the load/poll lifecycle, and all mutating actions. Views consume the returned
// object and stay presentational. `t` is passed in so fallback error/toast text
// is localized without the hook reaching into React context itself.
export function useDashboardData(t: TranslateFn) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [lb, setLB] = useState<LoadBalanceStatus | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [quotas, setQuotas] = useState<Record<string, QuotaResult>>({});
  const [clients, setClients] = useState<Client[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  // Empty string = "all pools" (the platform-wide admin view).
  const [selectedWorkspace, setSelectedWorkspace] = useState("");
  const [refreshQueue, setRefreshQueue] = useState<RefreshQueueItem[]>([]);
  const [dispatches, setDispatches] = useState<DispatchEvent[]>([]);
  const [personal, setPersonal] = useState<PersonalPayload | null>(null);
  const [accessMode, setAccessMode] = useState<AccessMode>("unknown");
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [createdClientToken, setCreatedClientToken] = useState("");
  const quotaAutoKeyRef = useRef("");

  const loadPersonal = useCallback(async () => {
    const payload = await apiJSON<PersonalPayload>("/api/me");
    setPersonal(payload);
    setAccessMode(payload.admin ? "admin" : "personal");
    return payload;
  }, []);

  const loadAll = useCallback(
    async (preferredId?: string) => {
      setLoading(true);
      try {
        const wsQuery = selectedWorkspace ? `?workspace=${encodeURIComponent(selectedWorkspace)}` : "";
        const [metaData, accountData, lbData, clientData, queueData, workspaceData] = await Promise.all([
          apiJSON<Meta>("/api/meta"),
          apiJSON<Account[]>("/api/accounts"),
          apiJSON<LoadBalanceStatus>(`/api/lb/status${wsQuery}`),
          apiJSON<Client[]>("/api/clients"),
          apiJSON<RefreshQueueItem[]>("/api/refresh-queue"),
          apiJSON<{ workspaces: Workspace[] }>("/api/workspaces"),
        ]);
        const dispatchData = await apiJSON<DispatchEvent[]>("/api/dispatches?limit=80");
        setMeta(metaData);
        setAccounts(accountData);
        setLB(lbData);
        setClients(clientData);
        setRefreshQueue(queueData);
        setWorkspaces(workspaceData.workspaces || []);
        setDispatches(dispatchData);
        setSelectedId((current) => {
          const target = preferredId ?? current;
          if (accountData.some((account) => account.id === target)) return target;
          return accountData.find((account) => account.active)?.id || accountData[0]?.id || "";
        });
        setAccessMode("admin");
        void loadPersonal().catch(() => undefined);
      } catch (error) {
        try {
          const payload = await loadPersonal();
          if (!payload.admin) setMessage("");
        } catch {
          setAccessMode("unknown");
          setMessage(error instanceof Error ? error.message : t("加载失败", "Load failed"));
        }
      } finally {
        setLoading(false);
      }
    },
    [loadPersonal, selectedWorkspace, t],
  );

  const fetchQuota = useCallback(
    async (id: string, quiet = false) => {
      setQuotas((current) => ({ ...current, [id]: { status: "loading" } }));
      try {
        const result = await apiJSON<QuotaResult>(`/api/accounts/${encodeURIComponent(id)}/quota`);
        setQuotas((current) => ({ ...current, [id]: result }));
        const queue = await apiJSON<RefreshQueueItem[]>("/api/refresh-queue");
        setRefreshQueue(queue);
        if (!quiet) setMessage(t("配额已刷新", "Quota refreshed"));
      } catch (error) {
        const detail = error instanceof Error ? error.message : t("配额刷新失败", "Quota failed");
        setQuotas((current) => ({ ...current, [id]: { status: "error", detail } }));
        if (!quiet) setMessage(detail);
      }
    },
    [t],
  );

  const withBusy = useCallback(
    async (action: () => Promise<void>) => {
      setBusy(true);
      try {
        await action();
      } catch (error) {
        setMessage(error instanceof Error ? error.message : t("操作失败", "Action failed"));
      } finally {
        setBusy(false);
      }
    },
    [t],
  );

  // ---- Lifecycle: initial load + 3-minute quota polling -------------------
  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  useEffect(() => {
    if (loading || !accounts.length) return;
    const key = accounts.map((account) => `${account.id}:${account.authPresent ? "1" : "0"}`).join("|");
    if (quotaAutoKeyRef.current === key) return;
    quotaAutoKeyRef.current = key;
    for (const account of accounts) {
      if (account.authPresent) void fetchQuota(account.id, true);
    }
  }, [accounts, loading, fetchQuota]);

  useEffect(() => {
    if (loading || !accounts.some((account) => account.authPresent)) return;
    const timer = window.setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      for (const account of accounts) {
        if (account.authPresent) void fetchQuota(account.id, true);
      }
    }, 180_000);
    return () => window.clearInterval(timer);
  }, [accounts, loading, fetchQuota]);

  // ---- Mutations ----------------------------------------------------------
  const saveAccount = useCallback(
    (id: string, draft: AccountDraft) =>
      withBusy(async () => {
        await apiJSON(`/api/accounts/${encodeURIComponent(id)}/label`, {
          method: "PATCH",
          body: JSON.stringify({ label: draft.label }),
        });
        await apiJSON(`/api/accounts/${encodeURIComponent(id)}/status`, {
          method: "PATCH",
          body: JSON.stringify({ status: draft.status }),
        });
        await apiJSON(`/api/accounts/${encodeURIComponent(id)}/owner`, {
          method: "PATCH",
          body: JSON.stringify({ ownerMode: draft.ownerMode, ownerClientId: draft.ownerClientId || "" }),
        });
        setMessage(t("账号已保存", "Account saved"));
        await loadAll(id);
      }),
    [withBusy, loadAll, t],
  );

  const deleteAccount = useCallback(
    (account: Account) =>
      withBusy(async () => {
        if (!window.confirm(`Delete ${accountName(account)}? This removes the managed snapshot only.`)) return;
        await apiJSON(`/api/accounts/${encodeURIComponent(account.id)}`, { method: "DELETE" });
        setSelectedId("");
        setMessage(t("账号已删除", "Account deleted"));
        await loadAll("");
      }),
    [withBusy, loadAll, t],
  );

  const uploadFiles = useCallback(
    (files: FileList) =>
      withBusy(async () => {
        const file = files[0];
        if (!file) return;
        const text = await file.text();
        const account = await apiJSON<Account>("/api/accounts/import-json", {
          method: "POST",
          body: text,
        });
        setSelectedId(account.id);
        setMessage(`${t("已导入", "Imported")} ${accountName(account)}`);
        await loadAll(account.id);
      }),
    [withBusy, loadAll, t],
  );

  const createClient = useCallback(
    (label: string) =>
      withBusy(async () => {
        const result = await apiJSON<{ client: Client; token: string }>("/api/clients", {
          method: "POST",
          body: JSON.stringify({ label }),
        });
        setCreatedClientToken(result.token);
        setMessage(`${t("已创建", "Created")} ${result.client.id}`);
        await loadAll();
      }),
    [withBusy, loadAll, t],
  );

  const revokeClient = useCallback(
    (id: string) =>
      withBusy(async () => {
        if (!window.confirm(`Revoke ${id}?`)) return;
        await apiJSON(`/api/clients/${encodeURIComponent(id)}`, { method: "DELETE" });
        setMessage(`${t("已吊销", "Revoked")} ${id}`);
        await loadAll();
      }),
    [withBusy, loadAll, t],
  );

  // ---- Workspace actions --------------------------------------------------
  const createWorkspace = useCallback(
    (name: string) =>
      withBusy(async () => {
        const ws = await apiJSON<Workspace>("/api/workspaces", {
          method: "POST",
          body: JSON.stringify({ name }),
        });
        setMessage(`${t("已创建工作区", "Workspace created")} ${ws.id}`);
        await loadAll();
      }),
    [withBusy, loadAll, t],
  );

  const listMembers = useCallback(async (workspaceId: string) => {
    const resp = await apiJSON<{ members: Membership[] }>(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/members`,
    );
    return resp.members || [];
  }, []);

  const setMember = useCallback(
    (workspaceId: string, clientId: string, role: WorkspaceRole) =>
      withBusy(async () => {
        await apiJSON(`/api/workspaces/${encodeURIComponent(workspaceId)}/members`, {
          method: "POST",
          body: JSON.stringify({ clientId, role }),
        });
        setMessage(`${t("成员已更新", "Member updated")} ${clientId}`);
      }),
    [withBusy, t],
  );

  const removeMember = useCallback(
    (workspaceId: string, clientId: string) =>
      withBusy(async () => {
        await apiJSON(
          `/api/workspaces/${encodeURIComponent(workspaceId)}/members/${encodeURIComponent(clientId)}`,
          { method: "DELETE" },
        );
        setMessage(`${t("成员已移除", "Member removed")} ${clientId}`);
      }),
    [withBusy, t],
  );

  const applyToken = useCallback(
    async (token: string) => {
      saveCloudToken(token);
      setMessage(t("令牌已保存", "Token saved"));
      await loadAll();
    },
    [loadAll, t],
  );

  const clearToken = useCallback(async () => {
    saveCloudToken("");
    setPersonal(null);
    setAccounts([]);
    setClients([]);
    setRefreshQueue([]);
    setDispatches([]);
    setAccessMode("unknown");
    setMessage(t("令牌已清除", "Token cleared"));
  }, [t]);

  // ---- Derived values -----------------------------------------------------
  const selected = useMemo(() => accounts.find((account) => account.id === selectedId), [accounts, selectedId]);
  const readyCount = useMemo(() => accounts.filter((account) => account.status === "ready").length, [accounts]);
  const activeAccount = useMemo(() => accounts.find((account) => account.active), [accounts]);
  const eligibleCount = lb?.eligible.length ?? 0;
  const excludedCount = lb?.excluded.length ?? 0;
  const lbTotalCount = eligibleCount + excludedCount;
  const lbEligiblePercent = lbTotalCount ? Math.round((eligibleCount / lbTotalCount) * 100) : 0;
  const lbAccounts = useMemo(() => [...(lb?.eligible || []), ...(lb?.excluded || [])], [lb]);
  const activeClientCount = useMemo(() => clients.filter((client) => client.active).length, [clients]);
  const personalUsage = useMemo<AccountUsage[]>(() => {
    if (!personal?.usage) return [];
    if (Array.isArray(personal.usage)) return personal.usage;
    return Object.values(personal.usage);
  }, [personal]);
  const refreshByAccount = useMemo(() => {
    const map = new Map<string, RefreshQueueItem>();
    for (const item of refreshQueue) map.set(item.accountId, item);
    return map;
  }, [refreshQueue]);
  const latestDispatchByAccount = useMemo(() => {
    const map = new Map<string, DispatchEvent>();
    for (const item of dispatches) {
      if (!map.has(item.accountId)) map.set(item.accountId, item);
    }
    return map;
  }, [dispatches]);

  return {
    // server state
    accounts,
    meta,
    lb,
    quotas,
    clients,
    workspaces,
    refreshQueue,
    dispatches,
    personal,
    accessMode,
    // ui-ish shared state
    selectedId,
    setSelectedId,
    selectedWorkspace,
    setSelectedWorkspace,
    message,
    setMessage,
    loading,
    busy,
    createdClientToken,
    // actions
    loadAll,
    fetchQuota,
    saveAccount,
    deleteAccount,
    uploadFiles,
    createClient,
    revokeClient,
    createWorkspace,
    listMembers,
    setMember,
    removeMember,
    applyToken,
    clearToken,
    // derived
    selected,
    readyCount,
    activeAccount,
    eligibleCount,
    excludedCount,
    lbTotalCount,
    lbEligiblePercent,
    lbAccounts,
    activeClientCount,
    personalUsage,
    refreshByAccount,
    latestDispatchByAccount,
  };
}

export type DashboardData = ReturnType<typeof useDashboardData>;
