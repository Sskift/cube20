import type { ChangeEvent, ReactNode } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Avatar,
  Button,
  Card,
  Chip,
  Input,
  ProgressBar,
  Skeleton,
} from "@heroui/react";
import {
  CheckCircle2,
  Copy,
  Database,
  FileJson,
  Gauge,
  Info,
  KeyRound,
  LogOut,
  PanelRightClose,
  PanelRightOpen,
  RefreshCw,
  Route,
  Save,
  ShieldCheck,
  Trash2,
  UploadCloud,
  UserRound,
  Users,
} from "lucide-react";

import { QuotaOverview } from "./views/QuotaOverview";

type AccountStatus = "ready" | "recovering" | "drain" | "disabled";
type AccountOwnerMode = "cloud" | "client";

interface Account {
  id: string;
  label: string;
  plan: string;
  status: AccountStatus;
  codexHome: string;
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  generation?: number;
  leaseId?: string;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
  authPresent: boolean;
  authPath: string;
  configPresent: boolean;
  configPath: string;
  active: boolean;
  leaseActive?: boolean;
}

interface Meta {
  statePath: string;
  settingsPath: string;
  accountsDir: string;
  liveCodexHome: string;
  liveAuthPresent: boolean;
  liveConfigPresent: boolean;
}

interface QuotaItem {
  label: string;
  usedPercent: number;
  usedDisplay: string;
  remainingDisplay: string;
  resetsAt?: string;
}

interface QuotaResult {
  status: string;
  plan?: string;
  source?: string;
  detail?: string;
  quotas?: QuotaItem[];
}

interface UsageToken {
  total: number;
  input: number;
  cachedInput: number;
  output: number;
}

interface ModelUsage {
  model: string;
  today: UsageToken;
  sevenDays: UsageToken;
  allTime: UsageToken;
  latestAt?: string;
}

interface AccountUsage {
  accountId: string;
  clientId?: string;
  updatedAt: string;
  latestAt?: string;
  latestModel?: string;
  today: UsageToken;
  sevenDays: UsageToken;
  allTime: UsageToken;
  models?: ModelUsage[];
}

interface DispatchEvent {
  id: string;
  leaseId: string;
  accountId: string;
  accountLabel?: string;
  clientId?: string;
  clientLabel?: string;
  holder?: string;
  event: string;
  generation?: number;
  createdAt: string;
  startedAt?: string;
  expiresAt?: string;
}

interface Client {
  id: string;
  label: string;
  createdAt: string;
  lastSeenAt?: string;
  active: boolean;
}

interface RefreshQueueItem {
  accountId: string;
  label: string;
  status: AccountStatus;
  authPresent: boolean;
  updatedAt?: string;
  resetsAt?: string;
  remainingDisplay?: string;
  remainingPercent?: number;
  usedPercent?: number;
  quotaStatus?: string;
  refreshOrderReason?: string;
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  quotaSource?: string;
  quotaReporterClientId?: string;
  leaseActive?: boolean;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
}

interface LoadBalanceAccount {
  id: string;
  label: string;
  status: AccountStatus;
  authPresent: boolean;
  configPresent: boolean;
  active: boolean;
  codexHome: string;
  ownerMode?: AccountOwnerMode;
  ownerClientId?: string;
  generation?: number;
  leaseActive?: boolean;
  leaseClientId?: string;
  leaseHolder?: string;
  leaseExpiresAt?: string;
  eligible: boolean;
  reason?: string;
  quotaStatus?: string;
  quotaRemainingDisplay?: string;
  quotaRemainingPercent?: number;
  quotaUsedPercent?: number;
  quotaResetsAt?: string;
  quotaUpdatedAt?: string;
  quotaScore?: number;
}

interface LoadBalanceStatus {
  policy: string;
  statePath: string;
  lastAccountId: string;
  eligible: LoadBalanceAccount[];
  excluded: LoadBalanceAccount[];
}

interface PersonalPayload {
  mode: "admin" | "client";
  admin: boolean;
  client?: Client;
  clients?: Client[];
  usage?: AccountUsage[] | Record<string, AccountUsage>;
  dispatches?: DispatchEvent[];
  totals?: {
    today: UsageToken;
    sevenDays: UsageToken;
    allTime: UsageToken;
  };
  refreshQueue?: RefreshQueueItem[];
}

type AccessMode = "unknown" | "admin" | "personal";
type DashboardView = "accounts" | "load-balancer" | "people" | "import" | "overview";

const CLOUD_TOKEN_KEY = "cube20.cloudToken";
let cloudTokenSynced = false;

function cloudToken() {
  if (typeof window === "undefined") return "";
  if (!cloudTokenSynced) {
    cloudTokenSynced = true;
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token");
    if (token) {
      window.localStorage.setItem(CLOUD_TOKEN_KEY, token);
      params.delete("token");
      const nextQuery = params.toString();
      const nextURL = `${window.location.pathname}${nextQuery ? `?${nextQuery}` : ""}${window.location.hash}`;
      window.history.replaceState(null, "", nextURL);
    }
  }
  return window.localStorage.getItem(CLOUD_TOKEN_KEY) || "";
}

function saveCloudToken(token: string) {
  if (typeof window === "undefined") return;
  cloudTokenSynced = true;
  const trimmed = token.trim();
  if (trimmed) {
    window.localStorage.setItem(CLOUD_TOKEN_KEY, trimmed);
  } else {
    window.localStorage.removeItem(CLOUD_TOKEN_KEY);
  }
}

function cloudOrigin() {
  if (typeof window === "undefined") return "";
  return window.location.origin;
}

function maskSecret(value: string) {
  if (!value) return "-";
  if (value.length <= 14) return `${value.slice(0, 3)}...`;
  return `${value.slice(0, 10)}...${value.slice(-6)}`;
}

async function copyText(value: string) {
  if (!value || typeof navigator === "undefined" || !navigator.clipboard) return;
  await navigator.clipboard.writeText(value);
}

async function apiJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const token = cloudToken();
  if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(path, { ...init, headers });
  const text = await response.text();
  const data = text ? JSON.parse(text) : {};
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data as T;
}

function shortID(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}...${value.slice(-4)}` : value;
}

function tokens(value?: number) {
  if (!value) return "0";
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return Math.round(value).toString();
}

function shortTime(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function accountName(account?: Account) {
  if (!account) return "-";
  return account.label || shortID(account.id);
}

function quotaSummary(quota?: QuotaResult) {
  if (!quota) return { label: "Not checked", value: 0, color: "default" as const };
  if (quota.status === "loading") return { label: "Checking", value: 0, color: "accent" as const };
  if (quota.status === "supported" && quota.quotas?.length) {
    const primary = quota.quotas[0];
    return {
      label: `${primary.remainingDisplay} left`,
      value: Math.max(0, Math.min(100, 100 - primary.usedPercent)),
      color: primary.usedPercent > 80 ? ("danger" as const) : primary.usedPercent > 55 ? ("warning" as const) : ("success" as const),
    };
  }
  if (quota.status === "unsupported_api_key") return { label: "API key", value: 0, color: "warning" as const };
  if (quota.status === "refresh_token_invalidated") return { label: "Re-login", value: 0, color: "danger" as const };
  return { label: quota.status, value: 0, color: "default" as const };
}

function quotaHint(quota?: QuotaResult) {
  if (!quota) return "";
  if (quota.status === "refresh_token_invalidated") return "Stored token was rotated or revoked. Re-login this account or upload a fresh auth.json.";
  if (quota.status === "unsupported_api_key") return "API-key auth cannot expose subscription balance.";
  if (quota.status === "not_configured") return "auth.json is missing.";
  if (quota.status === "error") return quota.detail || "Quota check failed.";
  return "";
}

function AppLayout({
  aside,
  asideOpen,
  children,
  className = "",
  navbar,
  sidebar,
  sidebarOpen,
}: {
  aside?: ReactNode;
  asideDefaultSize?: number;
  asideMaxSize?: number;
  asideMinSize?: number;
  asideMobile?: string;
  asideOpen?: boolean;
  asideResizable?: boolean;
  children: ReactNode;
  className?: string;
  navbar?: ReactNode;
  onAsideOpenChange?: (open: boolean) => void;
  onSidebarOpenChange?: (open: boolean) => void;
  scrollMode?: string;
  sidebar?: ReactNode;
  sidebarCollapsible?: string;
  sidebarDefaultSize?: number;
  sidebarMaxSize?: number;
  sidebarMinSize?: number;
  sidebarOpen?: boolean;
  sidebarResizable?: boolean;
  sidebarVariant?: string;
}) {
  return (
    <div className={`flex min-h-0 overflow-hidden ${className}`}>
      {sidebar && sidebarOpen !== false && <aside className="hidden w-[17rem] shrink-0 lg:block">{sidebar}</aside>}
      <div className="flex min-w-0 flex-1 flex-col">
        {navbar}
        <main className="min-h-0 flex-1 overflow-auto">{children}</main>
      </div>
      {aside && asideOpen && <aside className="hidden w-[24rem] shrink-0 border-l border-slate-200 xl:block">{aside}</aside>}
    </div>
  );
}

const DropZone = Object.assign(
  function DropZoneRoot({ children }: { children: ReactNode }) {
    return <div>{children}</div>;
  },
  {
    Area({ children, className = "" }: { children: ReactNode; className?: string }) {
      return <div className={className}>{children}</div>;
    },
    Icon({ children }: { children: ReactNode }) {
      return <div className="mb-2 flex justify-center text-slate-500">{children}</div>;
    },
    Label({ children }: { children: ReactNode }) {
      return <div className="text-sm font-semibold text-slate-900">{children}</div>;
    },
    Description({ children }: { children: ReactNode }) {
      return <div className="mt-1 text-xs text-slate-500">{children}</div>;
    },
    Input({ accept, onSelect }: { accept?: string; onSelect: (files: FileList) => void }) {
      return (
        <input
          accept={accept}
          className="mt-3 text-sm text-slate-600"
          type="file"
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            if (event.currentTarget.files) onSelect(event.currentTarget.files);
          }}
        />
      );
    },
  },
);

const EmptyState = Object.assign(
  function EmptyStateRoot({ children, className = "" }: { children: ReactNode; className?: string; size?: string }) {
    return <div className={`flex flex-col items-center justify-center text-center ${className}`}>{children}</div>;
  },
  {
    Media({ children }: { children: ReactNode; variant?: string }) {
      return <div className="mb-3 grid h-12 w-12 place-items-center rounded-lg bg-slate-100 text-slate-500">{children}</div>;
    },
    Title({ children }: { children: ReactNode }) {
      return <div className="text-sm font-semibold text-slate-950">{children}</div>;
    },
    Description({ children }: { children: ReactNode }) {
      return <div className="mt-1 max-w-sm text-xs text-slate-500">{children}</div>;
    },
  },
);

const KPI = Object.assign(
  function KPIRoot({ children, className = "" }: { children: ReactNode; className?: string }) {
    return <div className={`rounded-lg p-4 ${className}`}>{children}</div>;
  },
  {
    Header({ children }: { children: ReactNode }) {
      return <div className="mb-3 flex items-center gap-2">{children}</div>;
    },
    Icon({ children, status }: { children: ReactNode; status: "success" | "warning" | "danger" }) {
      const color = status === "success" ? "text-emerald-600 bg-emerald-50" : status === "warning" ? "text-amber-600 bg-amber-50" : "text-rose-600 bg-rose-50";
      return <div className={`grid h-8 w-8 place-items-center rounded-md ${color}`}>{children}</div>;
    },
    Title({ children }: { children: ReactNode }) {
      return <div className="text-xs font-medium uppercase text-slate-500">{children}</div>;
    },
    Content({ children }: { children: ReactNode }) {
      return <div>{children}</div>;
    },
    Value({ children }: { children: ReactNode; value: number }) {
      return <div className="text-2xl font-semibold text-slate-950">{children}</div>;
    },
  },
);

const NativeSelect = Object.assign(
  function NativeSelectRoot({ children }: { children: ReactNode; fullWidth?: boolean; variant?: string }) {
    return <div>{children}</div>;
  },
  {
    Trigger({ children, onChange, value }: { children: ReactNode; onChange: (event: ChangeEvent<HTMLSelectElement>) => void; value: string }) {
      return (
        <select
          className="h-10 w-full rounded-md border border-slate-200 bg-white px-3 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-400"
          value={value}
          onChange={onChange}
        >
          {children}
        </select>
      );
    },
    Option({ children, value }: { children: ReactNode; value: string }) {
      return <option value={value}>{children}</option>;
    },
  },
);

export default function App() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [lb, setLB] = useState<LoadBalanceStatus | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [label, setLabel] = useState("");
  const [status, setStatus] = useState<AccountStatus>("ready");
  const [ownerMode, setOwnerMode] = useState<AccountOwnerMode>("cloud");
  const [quotas, setQuotas] = useState<Record<string, QuotaResult>>({});
  const [clients, setClients] = useState<Client[]>([]);
  const [refreshQueue, setRefreshQueue] = useState<RefreshQueueItem[]>([]);
  const [dispatches, setDispatches] = useState<DispatchEvent[]>([]);
  const [personal, setPersonal] = useState<PersonalPayload | null>(null);
  const [accessMode, setAccessMode] = useState<AccessMode>("unknown");
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [activeView, setActiveView] = useState<DashboardView>("accounts");
  const [clientLabel, setClientLabel] = useState("");
  const [createdClientToken, setCreatedClientToken] = useState("");
  const [tokenInput, setTokenInput] = useState(() => cloudToken());
  const quotaAutoKeyRef = useRef("");

  const selected = useMemo(() => accounts.find((account) => account.id === selectedId), [accounts, selectedId]);
  const readyCount = accounts.filter((account) => account.status === "ready").length;
  const activeAccount = accounts.find((account) => account.active);
  const eligibleCount = lb?.eligible.length ?? 0;
  const excludedCount = lb?.excluded.length ?? 0;
  const lbTotalCount = eligibleCount + excludedCount;
  const lbEligiblePercent = lbTotalCount ? Math.round((eligibleCount / lbTotalCount) * 100) : 0;
  const lbAccounts = useMemo(() => [...(lb?.eligible || []), ...(lb?.excluded || [])], [lb]);
  const activeClientCount = clients.filter((client) => client.active).length;
  const personalUsage = useMemo(() => {
    if (!personal?.usage) return [] as AccountUsage[];
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
  const [asideOpen, setAsideOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(() => (typeof window === "undefined" ? true : window.innerWidth >= 1180));
  const [compactShell, setCompactShell] = useState(() => (typeof window === "undefined" ? false : window.innerWidth < 1180));

  async function loadPersonal() {
    const payload = await apiJSON<PersonalPayload>("/api/me");
    setPersonal(payload);
    setAccessMode(payload.admin ? "admin" : "personal");
    return payload;
  }

  async function loadAll(preferredId = selectedId) {
    setLoading(true);
    try {
      const [metaData, accountData, lbData, clientData, queueData] = await Promise.all([
        apiJSON<Meta>("/api/meta"),
        apiJSON<Account[]>("/api/accounts"),
        apiJSON<LoadBalanceStatus>("/api/lb/status"),
        apiJSON<Client[]>("/api/clients"),
        apiJSON<RefreshQueueItem[]>("/api/refresh-queue"),
      ]);
      const dispatchData = await apiJSON<DispatchEvent[]>("/api/dispatches?limit=80");
      setMeta(metaData);
      setAccounts(accountData);
      setLB(lbData);
      setClients(clientData);
      setRefreshQueue(queueData);
      setDispatches(dispatchData);
      if (!accountData.some((account) => account.id === preferredId)) {
        setSelectedId(accountData.find((account) => account.active)?.id || accountData[0]?.id || "");
      }
      setAccessMode("admin");
      void loadPersonal().catch(() => undefined);
    } catch (error) {
      try {
        const payload = await loadPersonal();
        if (!payload.admin) setMessage("");
      } catch {
        setAccessMode("unknown");
        setMessage(error instanceof Error ? error.message : "Load failed");
      }
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadAll("");
  }, []);

  useEffect(() => {
    function syncShellToViewport() {
      const wide = window.innerWidth >= 1180;
      setCompactShell(!wide);
      setSidebarOpen(wide);
      if (!wide) setAsideOpen(false);
    }
    syncShellToViewport();
    window.addEventListener("resize", syncShellToViewport);
    return () => window.removeEventListener("resize", syncShellToViewport);
  }, []);

  useEffect(() => {
    if (!selected) return;
    setLabel(selected.label || "");
    setStatus(selected.status);
    setOwnerMode(selected.ownerMode || "cloud");
  }, [selected]);

  useEffect(() => {
    if (loading || !accounts.length) return;
    const key = accounts.map((account) => `${account.id}:${account.authPresent ? "1" : "0"}`).join("|");
    if (quotaAutoKeyRef.current === key) return;
    quotaAutoKeyRef.current = key;
    for (const account of accounts) {
      if (account.authPresent) void fetchQuota(account.id, true);
    }
  }, [accounts, loading]);

  useEffect(() => {
    if (loading || !accounts.some((account) => account.authPresent)) return;
    const timer = window.setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      for (const account of accounts) {
        if (account.authPresent) void fetchQuota(account.id, true);
      }
    }, 180_000);
    return () => window.clearInterval(timer);
  }, [accounts, loading]);

  function selectView(view: DashboardView) {
    setActiveView(view);
    if (compactShell) setSidebarOpen(false);
  }

  async function withBusy(action: () => Promise<void>) {
    setBusy(true);
    try {
      await action();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Action failed");
    } finally {
      setBusy(false);
    }
  }

  async function saveAccount() {
    if (!selected) return;
    await withBusy(async () => {
      await apiJSON(`/api/accounts/${encodeURIComponent(selected.id)}/label`, {
        method: "PATCH",
        body: JSON.stringify({ label }),
      });
      await apiJSON(`/api/accounts/${encodeURIComponent(selected.id)}/status`, {
        method: "PATCH",
        body: JSON.stringify({ status }),
      });
      await apiJSON(`/api/accounts/${encodeURIComponent(selected.id)}/owner`, {
        method: "PATCH",
        body: JSON.stringify({ ownerMode, ownerClientId: selected.ownerClientId || "" }),
      });
      setMessage("Account saved");
      await loadAll(selected.id);
    });
  }

  async function deleteAccount() {
    if (!selected) return;
    if (!window.confirm(`Delete ${accountName(selected)}? This removes the managed snapshot only.`)) return;
    await withBusy(async () => {
      await apiJSON(`/api/accounts/${encodeURIComponent(selected.id)}`, { method: "DELETE" });
      setSelectedId("");
      setMessage("Account deleted");
      await loadAll("");
    });
  }

  async function uploadFiles(files: FileList) {
    const file = files[0];
    if (!file) return;
    await withBusy(async () => {
      const text = await file.text();
      const account = await apiJSON<Account>("/api/accounts/import-json", {
        method: "POST",
        body: text,
      });
      setSelectedId(account.id);
      setMessage(`Imported ${accountName(account)}`);
      await loadAll(account.id);
    });
  }

  async function fetchQuota(id: string, quiet = false) {
    setQuotas((current) => ({ ...current, [id]: { status: "loading" } }));
    try {
      const result = await apiJSON<QuotaResult>(`/api/accounts/${encodeURIComponent(id)}/quota`);
      setQuotas((current) => ({ ...current, [id]: result }));
      const queue = await apiJSON<RefreshQueueItem[]>("/api/refresh-queue");
      setRefreshQueue(queue);
      if (!quiet) setMessage("Quota refreshed");
    } catch (error) {
      const detail = error instanceof Error ? error.message : "Quota failed";
      setQuotas((current) => ({ ...current, [id]: { status: "error", detail } }));
      if (!quiet) setMessage(detail);
    }
  }

  async function createClient() {
    await withBusy(async () => {
      const result = await apiJSON<{ client: Client; token: string }>("/api/clients", {
        method: "POST",
        body: JSON.stringify({ label: clientLabel }),
      });
      setCreatedClientToken(result.token);
      setClientLabel("");
      setMessage(`Created ${result.client.id}`);
      await loadAll(selectedId);
    });
  }

  async function revokeClient(id: string) {
    if (!window.confirm(`Revoke ${id}?`)) return;
    await withBusy(async () => {
      await apiJSON(`/api/clients/${encodeURIComponent(id)}`, { method: "DELETE" });
      setMessage(`Revoked ${id}`);
      await loadAll(selectedId);
    });
  }

  async function applyToken() {
    saveCloudToken(tokenInput);
    setMessage("Token saved");
    await loadAll(selectedId);
  }

  async function clearToken() {
    saveCloudToken("");
    setTokenInput("");
    setPersonal(null);
    setAccounts([]);
    setClients([]);
    setRefreshQueue([]);
    setDispatches([]);
    setAccessMode("unknown");
    setMessage("Token cleared");
  }

  const sidebar = (
    <div className="flex h-full min-h-0 flex-col border-r border-slate-200 bg-white">
      <div className="flex items-center gap-3 border-b border-slate-200 px-4 py-4">
        <div className="grid h-10 w-10 place-items-center rounded-xl bg-slate-950 text-white">
          <ShieldCheck size={20} />
        </div>
        <div className="min-w-0">
          <div className="text-base font-semibold text-slate-950">cube20</div>
          <div className="text-xs text-slate-500">Codex pool manager</div>
        </div>
      </div>
      <div className="flex flex-1 flex-col gap-1 px-3 py-4 text-sm">
        <NavItem
          icon={<Database size={17} />}
          label="Accounts"
          active={activeView === "accounts"}
          badge={accounts.length.toString()}
          onPress={() => selectView("accounts")}
        />
        <NavItem
          icon={<Route size={17} />}
          label="Load Balancer"
          active={activeView === "load-balancer"}
          badge={eligibleCount.toString()}
          onPress={() => selectView("load-balancer")}
        />
        <NavItem
          icon={<Gauge size={17} />}
          label="配额总览"
          active={activeView === "overview"}
          badge={refreshQueue.length.toString()}
          onPress={() => selectView("overview")}
        />
        <NavItem
          icon={<Users size={17} />}
          label="People"
          active={activeView === "people"}
          badge={activeClientCount.toString()}
          onPress={() => selectView("people")}
        />
        <NavItem icon={<FileJson size={17} />} label="Import auth" active={activeView === "import"} onPress={() => selectView("import")} />
      </div>
      <div className="border-t border-slate-200 p-3">
        <div className="rounded-lg bg-slate-50 p-3 text-xs text-slate-600">
          <div className="mb-1 font-medium text-slate-900">Live Codex</div>
          <div className="path-text font-mono">{meta?.liveCodexHome || "-"}</div>
        </div>
      </div>
    </div>
  );

  const navbar = (
    <div className="cube-navbar flex min-h-14 w-full items-center justify-between gap-2 border-b border-slate-200 bg-white px-3 py-2 sm:gap-3 md:flex-nowrap md:px-4">
      <div className="flex min-w-0 items-center gap-3">
        {!compactShell && (
          <button
            aria-label="Menu"
            className="grid h-9 w-9 place-items-center rounded-lg border border-slate-200 bg-white text-slate-700 shadow-sm lg:hidden"
            type="button"
            onClick={() => setSidebarOpen(true)}
          >
            <PanelRightOpen size={15} />
          </button>
        )}
        {compactShell && (
          <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-slate-950 text-white">
            <ShieldCheck size={18} />
          </div>
        )}
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-slate-950">{compactShell ? "cube20 accounts" : "Account inventory"}</div>
          <div className="hidden max-w-[min(44vw,34rem)] truncate text-xs text-slate-500 min-[760px]:block">
            {meta?.accountsDir || "Loading accounts directory"}
          </div>
        </div>
      </div>
      <div className="cube-navbar-actions flex shrink-0 items-center gap-1.5 sm:gap-2">
        <Chip className="hidden min-[900px]:inline-flex" color={meta?.liveAuthPresent ? "success" : "warning"} size="sm" variant="soft">
          live auth {meta?.liveAuthPresent ? "ready" : "missing"}
        </Chip>
        <button
          aria-label={asideOpen ? "Hide details" : "Details"}
          className="inline-flex h-8 items-center gap-2 rounded-md border border-slate-200 bg-white px-2.5 text-sm font-medium text-slate-700 shadow-sm transition-colors hover:bg-slate-50 min-[560px]:px-3"
          type="button"
          onClick={() => setAsideOpen((open) => !open)}
        >
          {asideOpen ? <PanelRightClose size={15} /> : <PanelRightOpen size={15} />}
          <span className="hidden min-[700px]:inline">Details</span>
        </button>
        <Button aria-label="Reload data" className="gap-2" size="sm" variant="secondary" onPress={() => loadAll()}>
          <RefreshCw size={15} />
          <span className="hidden min-[700px]:inline">Reload</span>
        </Button>
      </div>
    </div>
  );

  if (!loading && accessMode === "personal" && personal) {
    return (
      <PersonalDashboard
        busy={busy}
        message={message}
        personal={personal}
        tokenInput={tokenInput}
        usage={personalUsage}
        onApplyToken={applyToken}
        onClearToken={clearToken}
        onRefresh={() => loadAll("")}
        onTokenInput={setTokenInput}
      />
    );
  }

  if (!loading && accessMode === "unknown" && !accounts.length) {
    return (
      <TokenGate
        busy={busy}
        message={message}
        tokenInput={tokenInput}
        onApplyToken={applyToken}
        onTokenInput={setTokenInput}
      />
    );
  }

  return (
    <AppLayout
      aside={<DetailsPanel />}
      asideDefaultSize={30}
      asideMaxSize={38}
      asideMinSize={24}
      asideMobile="sheet"
      asideResizable={!compactShell}
      className="h-screen bg-slate-50"
      asideOpen={asideOpen}
      onAsideOpenChange={setAsideOpen}
      sidebarOpen={sidebarOpen}
      onSidebarOpenChange={setSidebarOpen}
      navbar={navbar}
      scrollMode="content"
      sidebar={compactShell ? undefined : sidebar}
      sidebarCollapsible="offcanvas"
      sidebarDefaultSize={16}
      sidebarMaxSize={22}
      sidebarMinSize={14}
      sidebarResizable={!compactShell}
      sidebarVariant="sidebar"
    >
      <div className="cube-content mx-auto flex w-full max-w-[1500px] flex-col gap-4 p-3 sm:p-4 lg:gap-5 lg:p-6">
        {activeView === "accounts" && (
          <>
            <div className="hidden grid-cols-2 gap-2 min-[640px]:gap-3 lg:grid xl:grid-cols-4">
              <MetricCard icon={<Database size={18} />} label="Accounts" value={accounts.length.toString()} status="success" />
              <MetricCard icon={<CheckCircle2 size={18} />} label="Ready Pool" value={readyCount.toString()} status="success" />
              <MetricCard
                icon={<Route size={18} />}
                label="Dispatches"
                value={dispatches.length.toString()}
                status={dispatches.length > 0 ? "success" : "warning"}
              />
              <MetricCard
                icon={<ShieldCheck size={18} />}
                label="Clients"
                value={`${activeClientCount}/${clients.length}`}
                status={activeClientCount > 0 ? "success" : "warning"}
              />
            </div>

            <section className="cube-view-panel">
              <Card className="overflow-hidden border border-slate-200 bg-white shadow-sm">
                <Card.Header className="cube-accounts-header border-b border-slate-200 px-4 py-3 sm:px-5 sm:py-4">
                  <div className="cube-accounts-title min-w-0">
                    <div className="flex min-w-0 items-center gap-2.5">
                      <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-slate-100 text-slate-700">
                        <Database size={16} />
                      </div>
                      <div className="min-w-0">
                        <h2 className="text-base font-semibold leading-5 text-slate-950">Accounts</h2>
                        <p className="path-text text-xs text-slate-500">
                          {accounts.length} profiles · Active: {accountName(activeAccount)}
                        </p>
                      </div>
                    </div>
                    <div className="cube-accounts-chips">
                      <Chip color="success" size="sm" variant="soft">
                        {readyCount} ready
                      </Chip>
                      <Chip color="accent" size="sm" variant="soft">
                        {eligibleCount} lb
                      </Chip>
                      <Chip color={activeClientCount > 0 ? "success" : "warning"} size="sm" variant="soft">
                        {activeClientCount} clients
                      </Chip>
                    </div>
                  </div>
                </Card.Header>
                <Card.Content className="p-0">
                  {loading ? (
                    <div className="space-y-3 p-5">
                      <Skeleton className="h-16 rounded-xl" />
                      <Skeleton className="h-16 rounded-xl" />
                      <Skeleton className="h-16 rounded-xl" />
                    </div>
                  ) : (
                    <div className="account-card-grid p-3 sm:p-4">
                      {accounts.length ? (
                        accounts.map((account) => (
                          <MobileAccountCard
                            key={account.id}
                            account={account}
                            isSelected={account.id === selectedId}
                            quota={quotas[account.id]}
                            refresh={refreshByAccount.get(account.id)}
                            dispatch={latestDispatchByAccount.get(account.id)}
                            onSelect={() => {
                              setSelectedId(account.id);
                              setAsideOpen(true);
                            }}
                          />
                        ))
                      ) : (
                        <EmptyState size="md" className="account-card-grid__empty py-8">
                          <EmptyState.Media variant="icon">
                            <Database size={24} />
                          </EmptyState.Media>
                          <EmptyState.Title>No accounts yet</EmptyState.Title>
                          <EmptyState.Description>Import your current Codex profile or upload an auth.json snapshot.</EmptyState.Description>
                        </EmptyState>
                      )}
                    </div>
                  )}
                </Card.Content>
              </Card>
            </section>
          </>
        )}

        {activeView === "load-balancer" && (
          <section className="cube-view-panel">
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1.1fr)_minmax(20rem,0.9fr)]">
              <Card className="border border-slate-200 bg-white shadow-sm">
                <Card.Header className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
                  <div className="min-w-0">
                    <h2 className="text-base font-semibold text-slate-950">Load balancer</h2>
                    <p className="text-xs text-slate-500">Quota-aware lease assignment for cube run.</p>
                  </div>
                  <Chip color={eligibleCount ? "success" : "danger"} variant="soft">
                    {eligibleCount} assignable
                  </Chip>
                </Card.Header>
                <Card.Content className="gap-4">
                  <div className="lb-summary-grid">
                    <div className="lb-pool-summary">
                      <div className="flex min-w-0 items-center justify-between gap-3">
                        <div className="min-w-0">
                          <div className="text-xs font-semibold uppercase text-slate-500">Pool readiness</div>
                          <div className="mt-1 text-2xl font-semibold text-slate-950">{eligibleCount}/{lbTotalCount || 0}</div>
                        </div>
                        <Chip color={eligibleCount ? "success" : "danger"} variant="soft">
                          {lbEligiblePercent}% in pool
                        </Chip>
                      </div>
                      <div className="lb-stack" aria-label="Load balancer pool split">
                        <span className="lb-stack-ready" style={{ width: `${lbEligiblePercent}%` }} />
                      </div>
                      <div className="mt-2 flex flex-wrap gap-2 text-xs text-slate-500">
                        <span>{eligibleCount} in pool</span>
                        <span>{excludedCount} out</span>
                        <span>{lb?.policy || "quota-aware"}</span>
                      </div>
                    </div>
                    <div className="lb-next-summary">
                      <div className="text-xs font-semibold uppercase text-slate-500">Next lease candidate</div>
                      {lb?.eligible[0] ? (
                        <div className="mt-2 flex min-w-0 items-center gap-3">
                          <QuotaRing value={lb.eligible[0].quotaRemainingPercent} label={lb.eligible[0].quotaRemainingDisplay} />
                          <div className="min-w-0">
                            <div className="truncate text-sm font-semibold text-slate-950">{lbAccountName(lb.eligible[0])}</div>
                            <div className="mt-1 truncate text-xs text-slate-500">
                              score {scoreLabel(lb.eligible[0].quotaScore)} · reset {shortTime(lb.eligible[0].quotaResetsAt)}
                            </div>
                          </div>
                        </div>
                      ) : (
                        <div className="mt-2 text-sm font-medium text-rose-700">No assignable account</div>
                      )}
                    </div>
                  </div>

                  <div>
                    <div className="lb-section-head">
                      <span>Routing map</span>
                      <Chip color={eligibleCount ? "success" : "danger"} size="sm" variant="soft">
                        {eligibleCount}/{lbTotalCount || 0} assignable
                      </Chip>
                    </div>
                    <RoutingMap accounts={lbAccounts} dispatchByAccount={latestDispatchByAccount} />
                  </div>

                  <div>
                    <div className="lb-section-head">
                      <span>In pool</span>
                      <Chip color={eligibleCount ? "success" : "danger"} size="sm" variant="soft">
                        {eligibleCount}
                      </Chip>
                    </div>
                    <div className="lb-account-grid">
                      {lb?.eligible.map((account) => (
                        <LoadBalanceAccountCard key={account.id} account={account} />
                      ))}
                      {!eligibleCount && <div className="lb-empty">No account currently has ready auth, no active lease, and available 5h quota.</div>}
                    </div>
                  </div>
                </Card.Content>
              </Card>

              <div className="grid min-w-0 grid-cols-1 gap-4">
                <Card className="border border-slate-200 bg-white shadow-sm">
                  <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
                    <h2 className="text-base font-semibold text-slate-950">Out of pool</h2>
                    <Chip color={excludedCount ? "warning" : "success"} variant="soft">
                      {excludedCount}
                    </Chip>
                  </Card.Header>
                  <Card.Content className="gap-3">
                    {lb?.excluded.map((account) => (
                      <LoadBalanceAccountCard key={account.id} account={account} />
                    ))}
                    {!excludedCount && <div className="lb-empty">Every cloud-owned account is currently assignable.</div>}
                  </Card.Content>
                </Card>

                <Card className="border border-slate-200 bg-white shadow-sm">
                  <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
                    <h2 className="text-base font-semibold text-slate-950">5h reset order</h2>
                    <Chip color="accent" variant="soft">
                      {refreshQueue.length}
                    </Chip>
                  </Card.Header>
                  <Card.Content className="gap-2">
                    {refreshQueue.slice(0, 8).map((item, index) => (
                      <RefreshQueueBar key={item.accountId} item={item} index={index} />
                    ))}
                    {!refreshQueue.length && <div className="lb-empty">No quota checks yet.</div>}
                  </Card.Content>
                </Card>

                <Card className="border border-slate-200 bg-white shadow-sm">
                  <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
                    <h2 className="text-base font-semibold text-slate-950">Dispatch history</h2>
                    <Chip color={dispatches.length ? "success" : "warning"} variant="soft">
                      {dispatches.length}
                    </Chip>
                  </Card.Header>
                  <Card.Content className="gap-2">
                    <DispatchTimeline dispatches={dispatches.slice(0, 10)} />
                  </Card.Content>
                </Card>
              </div>
            </div>
          </section>
        )}

        {activeView === "people" && (
          <section className="cube-view-panel">
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
              <Card className="border border-slate-200 bg-white shadow-sm">
                <Card.Header className="border-b border-slate-200 px-5 py-4">
                  <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                    <KeyRound size={17} />
                    New PAT
                  </h2>
                </Card.Header>
                <Card.Content className="gap-4">
                  <FieldLabel text="client label">
                    <Input
                      fullWidth
                      placeholder="liushiao-local"
                      value={clientLabel}
                      variant="secondary"
                      onChange={(event) => setClientLabel(event.currentTarget.value)}
                    />
                  </FieldLabel>
                  <Button className="gap-2" isDisabled={busy} variant="primary" onPress={createClient}>
                    <KeyRound size={15} />
                    Create PAT
                  </Button>
                  {createdClientToken && (
                    <div className="rounded-lg border border-emerald-200 bg-emerald-50 p-3">
                      <div className="mb-2 text-xs font-semibold uppercase text-emerald-700">Token</div>
                      <div className="path-text font-mono text-xs text-emerald-950">{createdClientToken}</div>
                      <div className="mt-3 grid grid-cols-1 gap-2">
                        <CopyLine
                          label="Dashboard"
                          value={`${cloudOrigin()}/?token=${createdClientToken}`}
                        />
                        <CopyLine
                          label="Local config"
                          value={`cube cloud config --server ${cloudOrigin()} --token ${createdClientToken}`}
                        />
                      </div>
                    </div>
                  )}
                </Card.Content>
              </Card>

              <Card className="border border-slate-200 bg-white shadow-sm">
                <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
                  <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                    <Users size={17} />
                    People
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
                              {client.active ? "active" : "revoked"}
                            </Chip>
                          </div>
                          <div className="mt-1 font-mono text-xs text-slate-500">{client.id}</div>
                          <div className="mt-1 text-xs text-slate-500">last seen {shortTime(client.lastSeenAt)}</div>
                        </div>
                        <Button
                          aria-label={`Revoke ${client.id}`}
                          className="gap-2"
                          isDisabled={busy || !client.active}
                          size="sm"
                          variant="danger-soft"
                          onPress={() => revokeClient(client.id)}
                        >
                          <Trash2 size={14} />
                          Revoke
                        </Button>
                      </div>
                    ))}
                    {!clients.length && <div className="px-4 py-6 text-sm text-slate-500">No clients</div>}
                  </div>
                </Card.Content>
              </Card>
            </div>
          </section>
        )}

        {activeView === "import" && (
          <section className="cube-view-panel">
            <Card className="border border-slate-200 bg-white shadow-sm">
              <Card.Header className="border-b border-slate-200 px-5 py-4">
                <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                  <UploadCloud size={17} />
                  Import auth.json
                </h2>
              </Card.Header>
              <Card.Content>
                <DropZone>
                  <DropZone.Area className="min-h-32 rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-5 text-center">
                    <DropZone.Icon>
                      <FileJson size={26} />
                    </DropZone.Icon>
                    <DropZone.Label>Drop or choose auth.json</DropZone.Label>
                    <DropZone.Description>Raw Codex auth.json or cube20 profile JSON</DropZone.Description>
                    <DropZone.Input accept=".json,application/json" onSelect={uploadFiles} />
                  </DropZone.Area>
                </DropZone>
              </Card.Content>
            </Card>
          </section>
        )}

        {activeView === "overview" && (
          <section className="cube-view-panel">
            <QuotaOverview queue={refreshQueue} />
          </section>
        )}

        {message && (
          <Card className="border border-teal-200 bg-teal-50 text-teal-900">
            <Card.Content className="flex flex-row items-start gap-2 p-4 text-sm">
              <Info size={16} className="mt-0.5 shrink-0" />
              <span>{message}</span>
            </Card.Content>
          </Card>
        )}
      </div>
    </AppLayout>
  );

  function DetailsPanel() {
    const selectedRefresh = selected ? refreshByAccount.get(selected.id) : undefined;
    const selectedDispatch = selected ? latestDispatchByAccount.get(selected.id) : undefined;
    return (
      <div className="flex h-full min-h-0 flex-col bg-white">
        <div className="border-b border-slate-200 px-5 py-4">
          <div className="text-sm font-semibold text-slate-950">Account detail</div>
          <div className="mt-1 text-xs text-slate-500">{selected ? `${selected.status} · ${selected.authPresent ? "auth ready" : "auth missing"}` : "No account opened"}</div>
        </div>
        <div className="flex-1 space-y-4 overflow-auto p-5">
          {selected ? (
            <>
              <Card className="border border-slate-200 shadow-none">
                <Card.Content className="gap-4">
                  <FieldLabel text="Nickname">
                    <Input
                      fullWidth
                      value={label}
                      variant="secondary"
                      onChange={(event) => setLabel(event.currentTarget.value)}
                    />
                  </FieldLabel>
                  <FieldLabel text="Pool status">
                    <NativeSelect fullWidth variant="secondary">
                      <NativeSelect.Trigger
                        value={status}
                        onChange={(event) => setStatus(event.currentTarget.value as AccountStatus)}
                      >
                        <NativeSelect.Option value="ready">ready</NativeSelect.Option>
                        <NativeSelect.Option value="recovering">recovering</NativeSelect.Option>
                        <NativeSelect.Option value="drain">drain</NativeSelect.Option>
                        <NativeSelect.Option value="disabled">disabled</NativeSelect.Option>
                      </NativeSelect.Trigger>
                    </NativeSelect>
                  </FieldLabel>
                  <FieldLabel text="Owner">
                    <NativeSelect fullWidth variant="secondary">
                      <NativeSelect.Trigger
                        value={ownerMode}
                        onChange={(event) => setOwnerMode(event.currentTarget.value as AccountOwnerMode)}
                      >
                        <NativeSelect.Option value="cloud">cloud</NativeSelect.Option>
                        <NativeSelect.Option value="client">client</NativeSelect.Option>
                      </NativeSelect.Trigger>
                    </NativeSelect>
                  </FieldLabel>
                  <Button className="gap-2" isDisabled={busy} variant="secondary" onPress={saveAccount}>
                    <Save size={15} />
                    Save
                  </Button>
                  <Button className="gap-2" isDisabled={busy} variant="danger-soft" onPress={deleteAccount}>
                    <Trash2 size={15} />
                    Delete account
                  </Button>
                </Card.Content>
              </Card>

              <Card className="border border-slate-200 shadow-none">
                <Card.Header className="border-b border-slate-100 px-4 py-3 text-sm font-semibold">Cloud signals</Card.Header>
                <Card.Content className="gap-3 text-xs">
                  <SignalLine label="5h quota" value={selectedRefresh?.remainingDisplay ? `${selectedRefresh.remainingDisplay} left` : selectedRefresh?.refreshOrderReason || "-"} />
                  <SignalLine label="5h reset" value={selectedRefresh?.resetsAt ? shortTime(selectedRefresh.resetsAt) : selectedRefresh?.refreshOrderReason || "-"} />
                  <SignalLine label="quota source" value={selectedRefresh?.quotaSource ? `${selectedRefresh.quotaSource}${selectedRefresh.quotaReporterClientId ? ` · ${selectedRefresh.quotaReporterClientId}` : ""}` : quotas[selected.id]?.source || "-"} />
                  <SignalLine label="generation" value={(selected.generation || 0).toString()} />
                  <SignalLine label="owner" value={selected.ownerMode === "client" ? `client ${selected.ownerClientId || "-"}` : "cloud"} />
                  <SignalLine label="lease" value={selected.leaseActive ? `${selected.leaseClientId || selected.leaseHolder || "client"} until ${shortTime(selected.leaseExpiresAt)}` : "-"} />
                </Card.Content>
              </Card>

              <Card className="border border-slate-200 shadow-none">
                <Card.Header className="border-b border-slate-100 px-4 py-3 text-sm font-semibold">Dispatch</Card.Header>
                <Card.Content className="gap-3 text-xs">
                  <SignalLine label="current lease" value={selected.leaseActive ? dispatchTarget(selected.leaseClientId, "", selected.leaseHolder) : "-"} />
                  <SignalLine label="lease expires" value={selected.leaseActive ? shortTime(selected.leaseExpiresAt) : "-"} />
                  <SignalLine label="last dispatch" value={selectedDispatch ? `${dispatchEventLabel(selectedDispatch.event)} · ${shortTime(selectedDispatch.createdAt)}` : "-"} />
                  <SignalLine label="sent to" value={selectedDispatch ? dispatchTarget(selectedDispatch.clientId, selectedDispatch.clientLabel, selectedDispatch.holder) : "-"} />
                </Card.Content>
              </Card>
            </>
          ) : (
            <EmptyState size="md" className="py-8">
              <EmptyState.Media variant="icon">
                <Database size={24} />
              </EmptyState.Media>
              <EmptyState.Title>Open an account</EmptyState.Title>
              <EmptyState.Description>Use the account grid to inspect auth files and route status.</EmptyState.Description>
            </EmptyState>
          )}
        </div>
      </div>
    );
  }
}

function TokenGate({
  busy,
  message,
  onApplyToken,
  onTokenInput,
  tokenInput,
}: {
  busy: boolean;
  message: string;
  onApplyToken: () => Promise<void>;
  onTokenInput: (value: string) => void;
  tokenInput: string;
}) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-slate-50 p-4">
      <Card className="w-full max-w-xl border border-slate-200 bg-white shadow-sm">
        <Card.Header className="border-b border-slate-200 px-5 py-4">
          <h1 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <ShieldCheck size={18} />
            cube20
          </h1>
        </Card.Header>
        <Card.Content className="gap-4">
          <FieldLabel text="admin token or PAT">
            <Input
              fullWidth
              value={tokenInput}
              variant="secondary"
              onChange={(event) => onTokenInput(event.currentTarget.value)}
            />
          </FieldLabel>
          <Button className="gap-2" isDisabled={busy || !tokenInput.trim()} variant="primary" onPress={onApplyToken}>
            <KeyRound size={15} />
            Continue
          </Button>
          {message && (
            <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900">
              {message}
            </div>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}

function PersonalDashboard({
  busy,
  message,
  onApplyToken,
  onClearToken,
  onRefresh,
  onTokenInput,
  personal,
  tokenInput,
  usage,
}: {
  busy: boolean;
  message: string;
  onApplyToken: () => Promise<void>;
  onClearToken: () => Promise<void>;
  onRefresh: () => Promise<void>;
  onTokenInput: (value: string) => void;
  personal: PersonalPayload;
  tokenInput: string;
  usage: AccountUsage[];
}) {
  const client = personal.client;
  const totals = personal.totals;
  const dispatches = personal.dispatches || [];
  const browserToken = cloudToken();
  const configCommand = `cube cloud config --server ${cloudOrigin()} --token ${browserToken || "<cube_pat_...>"}`;

  return (
    <AppLayout
      className="h-screen bg-slate-50"
      navbar={
        <div className="cube-navbar flex min-h-14 w-full items-center justify-between gap-3 border-b border-slate-200 bg-white px-4 py-2">
          <div className="flex min-w-0 items-center gap-3">
            <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-slate-950 text-white">
              <UserRound size={18} />
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-slate-950">My page</div>
              <div className="truncate text-xs text-slate-500">{client?.label || client?.id || "client"}</div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button aria-label="Reload data" className="gap-2" isDisabled={busy} size="sm" variant="secondary" onPress={onRefresh}>
              <RefreshCw size={15} />
              <span className="hidden sm:inline">Reload</span>
            </Button>
            <Button aria-label="Clear token" className="gap-2" isDisabled={busy} size="sm" variant="danger-soft" onPress={onClearToken}>
              <LogOut size={15} />
              <span className="hidden sm:inline">Token</span>
            </Button>
          </div>
        </div>
      }
    >
      <div className="cube-content mx-auto flex w-full max-w-6xl flex-col gap-4 p-3 sm:p-4 lg:gap-5 lg:p-6">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
          <MetricCard icon={<UserRound size={18} />} label="Client" value={client?.active ? "Active" : "Inactive"} status={client?.active ? "success" : "danger"} />
          <MetricCard icon={<Gauge size={18} />} label="7d Token Usage" value={tokens(totals?.sevenDays?.total)} status={(totals?.sevenDays?.total || 0) > 0 ? "success" : "warning"} />
          <MetricCard icon={<Route size={18} />} label="Dispatches" value={dispatches.length.toString()} status={dispatches.length ? "success" : "warning"} />
        </div>

        <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
          <Card className="border border-slate-200 bg-white shadow-sm">
            <Card.Header className="border-b border-slate-200 px-5 py-4">
              <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                <UserRound size={17} />
                Profile
              </h2>
            </Card.Header>
            <Card.Content className="gap-3 text-sm">
              <SignalLine label="client id" value={client?.id || "-"} />
              <SignalLine label="label" value={client?.label || "-"} />
              <SignalLine label="last seen" value={shortTime(client?.lastSeenAt)} />
              <SignalLine label="browser token" value={maskSecret(browserToken)} />
            </Card.Content>
          </Card>

          <Card className="border border-slate-200 bg-white shadow-sm">
            <Card.Header className="border-b border-slate-200 px-5 py-4">
              <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                <KeyRound size={17} />
                Access
              </h2>
            </Card.Header>
            <Card.Content className="gap-4">
              <FieldLabel text="token">
                <Input
                  fullWidth
                  value={tokenInput}
                  variant="secondary"
                  onChange={(event) => onTokenInput(event.currentTarget.value)}
                />
              </FieldLabel>
              <div className="flex flex-wrap gap-2">
                <Button className="gap-2" isDisabled={busy || !tokenInput.trim()} variant="primary" onPress={onApplyToken}>
                  <Save size={15} />
                  Save token
                </Button>
                <Button className="gap-2" variant="secondary" onPress={() => copyText(configCommand)}>
                  <Copy size={15} />
                  Copy config
                </Button>
              </div>
              <CopyLine label="Local config" value={configCommand} />
              <CopyLine label="Dashboard" value={`${cloudOrigin()}/?token=${browserToken || "<cube_pat_...>"}`} />
            </Card.Content>
          </Card>
        </div>

        <Card className="border border-slate-200 bg-white shadow-sm">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Route size={17} />
              Dispatches
            </h2>
            <Chip color={dispatches.length ? "success" : "warning"} variant="soft">
              {dispatches.length}
            </Chip>
          </Card.Header>
          <Card.Content className="gap-2">
            <DispatchTimeline dispatches={dispatches.slice(0, 10)} />
          </Card.Content>
        </Card>

        <Card className="border border-slate-200 bg-white shadow-sm">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Gauge size={17} />
              Usage by account
            </h2>
            <Chip color="accent" variant="soft">
              {tokens(totals?.allTime?.total)} all
            </Chip>
          </Card.Header>
          <Card.Content className="p-0">
            <div className="divide-y divide-slate-200">
              {usage.map((item) => (
                <div key={item.accountId} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3 text-sm">
                  <div className="min-w-0">
                    <div className="truncate font-semibold text-slate-950">{shortID(item.accountId)}</div>
                    <div className="truncate text-xs text-slate-500">{item.latestModel || item.models?.[0]?.model || "no model"} · {shortTime(item.latestAt || item.updatedAt)}</div>
                  </div>
                  <div className="text-right">
                    <div className="font-semibold text-slate-950">{tokens(item.sevenDays?.total)} 7d</div>
                    <div className="text-xs text-slate-500">{tokens(item.today?.total)} today</div>
                  </div>
                </div>
              ))}
              {!usage.length && <div className="px-4 py-6 text-sm text-slate-500">No usage yet</div>}
            </div>
          </Card.Content>
        </Card>

        {message && (
          <Card className="border border-teal-200 bg-teal-50 text-teal-900">
            <Card.Content className="flex flex-row items-start gap-2 p-4 text-sm">
              <Info size={16} className="mt-0.5 shrink-0" />
              <span>{message}</span>
            </Card.Content>
          </Card>
        )}
      </div>
    </AppLayout>
  );
}

function CopyLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-lg bg-slate-50 p-3">
      <div className="min-w-0">
        <div className="mb-1 text-[11px] font-semibold uppercase text-slate-400">{label}</div>
        <div className="path-text font-mono text-xs text-slate-700">{value}</div>
      </div>
      <Button aria-label={`Copy ${label}`} size="sm" variant="secondary" onPress={() => copyText(value)}>
        <Copy size={14} />
      </Button>
    </div>
  );
}

function clampUIPercent(value?: number) {
  if (typeof value !== "number" || Number.isNaN(value)) return 0;
  return Math.max(0, Math.min(100, value));
}

function lbAccountName(account: LoadBalanceAccount) {
  return account.label || shortID(account.id);
}

function scoreLabel(value?: number) {
  if (typeof value !== "number" || Number.isNaN(value)) return "-";
  return value.toFixed(1);
}

function quotaStatusLabel(value?: string) {
  switch (value) {
    case "refresh_token_invalidated":
      return "re-login";
    case "unsupported_api_key":
      return "api key";
    case "not_configured":
      return "missing";
    case "supported":
      return "checked";
    case "error":
      return "error";
    default:
      return value || "-";
  }
}

function dispatchEventLabel(event?: string) {
  switch (event) {
    case "claimed":
      return "dispatched";
    case "released":
      return "released";
    case "expired":
      return "expired";
    default:
      return event || "-";
  }
}

function dispatchTarget(clientId?: string, clientLabel?: string, holder?: string) {
  const label = clientLabel?.trim() || holder?.trim() || clientId?.trim();
  if (!label) return "-";
  if (clientId && label !== clientId) return `${label} · ${shortID(clientId)}`;
  return label;
}

function QuotaRing({ label, value }: { label?: string; value?: number }) {
  const remaining = clampUIPercent(value);
  const degrees = remaining * 3.6;
  return (
    <div
      className="lb-quota-ring"
      style={{ background: `conic-gradient(#10b981 ${degrees}deg, #e2e8f0 0deg)` }}
    >
      <span>{label || (value === undefined ? "-" : `${Math.round(remaining)}%`)}</span>
    </div>
  );
}

function LoadBalanceAccountCard({ account }: { account: LoadBalanceAccount }) {
  const remaining = clampUIPercent(account.quotaRemainingPercent);
  const quotaLabel = account.quotaRemainingDisplay || quotaStatusLabel(account.quotaStatus);

  return (
    <div className={`lb-account-card ${account.eligible ? "is-eligible" : "is-excluded"}`}>
      <div className="lb-account-top">
        <QuotaRing label={quotaLabel} value={account.quotaRemainingPercent} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <span className="truncate text-sm font-semibold text-slate-950">{lbAccountName(account)}</span>
            <Chip color={account.eligible ? "success" : "warning"} size="sm" variant="soft">
              {account.eligible ? "in pool" : "out"}
            </Chip>
            {account.leaseActive && (
              <Chip color="accent" size="sm" variant="soft">
                leased
              </Chip>
            )}
          </div>
          <div className="mt-1 font-mono text-xs text-slate-500">{shortID(account.id)}</div>
        </div>
      </div>

      <div className="lb-quota-line">
        <span>5h remaining</span>
        <strong>{quotaLabel}</strong>
      </div>
      <div className="lb-quota-track">
        <span style={{ width: `${remaining}%` }} />
      </div>

      <div className="lb-account-metrics">
        <div>
          <span>score</span>
          <strong>{scoreLabel(account.quotaScore)}</strong>
        </div>
        <div>
          <span>reset</span>
          <strong>{shortTime(account.quotaResetsAt)}</strong>
        </div>
        <div>
          <span>gen</span>
          <strong>{account.generation || 0}</strong>
        </div>
      </div>

      {account.reason && <div className="lb-reason">{account.reason}</div>}
      {account.leaseActive && (
        <div className="lb-reason is-active">
          sent to {dispatchTarget(account.leaseClientId, "", account.leaseHolder)} until {shortTime(account.leaseExpiresAt)}
        </div>
      )}
    </div>
  );
}

function RoutingMap({
  accounts,
  dispatchByAccount,
}: {
  accounts: LoadBalanceAccount[];
  dispatchByAccount: Map<string, DispatchEvent>;
}) {
  if (!accounts.length) {
    return <div className="lb-empty">No cloud-owned account is registered for load balancing.</div>;
  }

  return (
    <div className="lb-route-map">
      {accounts.map((account) => {
        const dispatch = dispatchByAccount.get(account.id);
        const remaining = clampUIPercent(account.quotaRemainingPercent);
        const quotaLabel = account.quotaRemainingDisplay || quotaStatusLabel(account.quotaStatus);
        const target = account.leaseActive
          ? dispatchTarget(account.leaseClientId, "", account.leaseHolder)
          : dispatch
            ? dispatchTarget(dispatch.clientId, dispatch.clientLabel, dispatch.holder)
            : "-";
        const targetLabel = account.leaseActive ? "current" : dispatch ? "last" : "sent to";

        return (
          <div key={account.id} className={`lb-route-row ${account.eligible ? "is-eligible" : "is-excluded"}`}>
            <div className="lb-route-main">
              <div className="lb-route-state" aria-hidden="true" />
              <div className="min-w-0">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="truncate text-sm font-semibold text-slate-950">{lbAccountName(account)}</span>
                  <Chip color={account.eligible ? "success" : "warning"} size="sm" variant="soft">
                    {account.eligible ? "in pool" : "out"}
                  </Chip>
                  {account.leaseActive && (
                    <Chip color="accent" size="sm" variant="soft">
                      leased
                    </Chip>
                  )}
                </div>
                <div className="mt-1 truncate font-mono text-xs text-slate-500">{shortID(account.id)}</div>
              </div>
            </div>

            <div className="lb-route-quota" aria-label={`${lbAccountName(account)} quota ${quotaLabel}`}>
              <span style={{ width: `${remaining}%` }} />
            </div>

            <div className="lb-route-facts">
              <SignalLine label="5h quota" value={quotaLabel} />
              <SignalLine label="score" value={scoreLabel(account.quotaScore)} />
              <SignalLine label="reset" value={shortTime(account.quotaResetsAt)} />
              <SignalLine label={targetLabel} value={target} />
            </div>

            {account.reason && <div className="lb-route-reason">{account.reason}</div>}
          </div>
        );
      })}
    </div>
  );
}

function DispatchTimeline({ dispatches }: { dispatches: DispatchEvent[] }) {
  if (!dispatches.length) {
    return <div className="lb-empty">No dispatches recorded yet.</div>;
  }
  return (
    <div className="dispatch-list">
      {dispatches.map((event) => (
        <div key={event.id} className={`dispatch-row event-${event.event || "unknown"}`}>
          <div className="dispatch-dot" />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center justify-between gap-2">
              <span className="truncate text-sm font-semibold text-slate-950">{event.accountLabel || shortID(event.accountId)}</span>
              <Chip color={event.event === "claimed" ? "success" : event.event === "expired" ? "danger" : "default"} size="sm" variant="soft">
                {dispatchEventLabel(event.event)}
              </Chip>
            </div>
            <div className="mt-1 truncate text-xs text-slate-500">
              to {dispatchTarget(event.clientId, event.clientLabel, event.holder)} · {shortTime(event.createdAt)}
            </div>
            <div className="mt-1 font-mono text-[11px] text-slate-400">{shortID(event.leaseId)}</div>
          </div>
        </div>
      ))}
    </div>
  );
}

function refreshQueueReason(item: RefreshQueueItem) {
  if (item.ownerMode === "client") {
    return `${item.refreshOrderReason || "client reported"}${item.quotaReporterClientId ? ` · ${item.quotaReporterClientId}` : ""}`;
  }
  if (item.leaseActive) return `leased by ${item.leaseClientId || "client"}`;
  return item.refreshOrderReason || item.quotaStatus || item.status;
}

function RefreshQueueBar({ index, item }: { index: number; item: RefreshQueueItem }) {
  const remaining = clampUIPercent(item.remainingPercent);
  return (
    <div className="lb-queue-row">
      <div className="lb-queue-rank">{index + 1}</div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center justify-between gap-2">
          <span className="truncate text-sm font-semibold text-slate-900">{item.label || shortID(item.accountId)}</span>
          <span className="shrink-0 text-xs font-semibold text-slate-700">{item.remainingDisplay || "-"}</span>
        </div>
        <div className="mt-1 truncate text-xs text-slate-500">
          {refreshQueueReason(item)} · {shortTime(item.leaseActive ? item.leaseExpiresAt : item.resetsAt)}
        </div>
        <div className="lb-queue-track">
          <span style={{ width: `${remaining}%` }} />
        </div>
      </div>
    </div>
  );
}

function NavItem({
  active,
  badge,
  icon,
  label,
  onPress,
}: {
  active?: boolean;
  badge?: string;
  icon: ReactNode;
  label: string;
  onPress: () => void;
}) {
  return (
    <button
      aria-current={active ? "page" : undefined}
      aria-pressed={active}
      className={`cube-nav-button flex h-10 w-full items-center gap-3 rounded-xl px-3 text-left text-sm ${
        active ? "bg-slate-950 text-white shadow-sm" : "text-slate-600 hover:bg-slate-100 hover:text-slate-950"
      }`}
      type="button"
      onClick={onPress}
    >
      <span className="grid h-6 w-6 place-items-center">{icon}</span>
      <span className="min-w-0 flex-1 truncate font-medium">{label}</span>
      {badge && (
        <span className={`rounded-full px-2 py-0.5 text-xs ${active ? "bg-white/15 text-white" : "bg-slate-200 text-slate-600"}`}>
          {badge}
        </span>
      )}
    </button>
  );
}

function MobileAccountCard({
  account,
  dispatch,
  isSelected,
  onSelect,
  quota,
  refresh,
}: {
  account: Account;
  dispatch?: DispatchEvent;
  isSelected: boolean;
  onSelect: () => void;
  quota?: QuotaResult;
  refresh?: RefreshQueueItem;
}) {
  const summary = quotaSummary(quota);
  const hint = quotaHint(quota);
  const fiveHour = refresh?.remainingDisplay || summary.label;

  return (
    <div
      className={`cube-mobile-account rounded-lg border bg-white p-3 shadow-sm ${
        isSelected ? "border-slate-900 ring-1 ring-slate-900" : "border-slate-200"
      }`}
    >
      <div className="flex min-w-0 items-start gap-3">
        <Avatar className="shrink-0" color="accent" size="md" variant="soft">
          <Avatar.Fallback>{(account.label || account.id).slice(0, 2).toUpperCase()}</Avatar.Fallback>
        </Avatar>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <span className="truncate text-sm font-semibold text-slate-950">{accountName(account)}</span>
            {account.active && (
              <Chip color="accent" size="sm" variant="soft">
                active
              </Chip>
            )}
            <Chip color={account.status === "ready" ? "success" : account.status === "recovering" || account.status === "drain" ? "warning" : "danger"} size="sm" variant="soft">
              {account.status}
            </Chip>
            {account.leaseActive && (
              <Chip color="accent" size="sm" variant="soft">
                leased
              </Chip>
            )}
          </div>
          <div className="mt-1 font-mono text-xs text-slate-500">{shortID(account.id)}</div>
        </div>
        <Button
          className="shrink-0 gap-1.5"
          size="sm"
          variant={isSelected ? "primary" : "secondary"}
          onPress={onSelect}
        >
          {isSelected ? <CheckCircle2 size={14} /> : <PanelRightOpen size={14} />}
          <span>Details</span>
        </Button>
      </div>

      <div className="mt-3 flex flex-wrap gap-1.5">
        <Chip color={account.authPresent ? "success" : "danger"} size="sm" variant="soft">
          auth
        </Chip>
        <Chip color={account.ownerMode === "client" ? "accent" : "default"} size="sm" variant="soft">
          {account.ownerMode || "cloud"}
        </Chip>
        <Chip color={refresh?.quotaStatus === "supported" ? "success" : "warning"} size="sm" variant="soft">
          5h {fiveHour}
        </Chip>
        <Chip color={account.leaseActive ? "accent" : "default"} size="sm" variant="soft">
          gen {account.generation || 0}
        </Chip>
      </div>

      {isSelected ? (
        <>
          <div className="mt-3 rounded-md bg-slate-50 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <span className="truncate text-xs font-medium text-slate-700">{summary.label}</span>
              <span className="text-xs text-slate-500">{shortTime(refresh?.updatedAt)}</span>
            </div>
            <ProgressBar aria-label="Quota remaining" color={summary.color} size="sm" value={summary.value} />
            {hint && <div className="quota-card-hint mt-2 text-xs leading-5 text-slate-500">{hint}</div>}
          </div>

          <div className="mt-3 flex items-center justify-between gap-2 rounded-md bg-slate-50 p-3 text-xs text-slate-600">
            {dispatch ? (
              <div className="min-w-0">
                <div className="font-semibold text-slate-900">{dispatchEventLabel(dispatch.event)} {shortTime(dispatch.createdAt)}</div>
                <div className="truncate">to {dispatchTarget(dispatch.clientId, dispatch.clientLabel, dispatch.holder)}</div>
              </div>
            ) : (
              <span className="font-medium text-slate-700">No dispatch yet</span>
            )}
          </div>

          <div className="mt-3 rounded-md bg-slate-50 p-2">
            <div className="mb-1 text-[11px] font-semibold uppercase leading-4 text-slate-400">5h refresh</div>
            <div className="text-xs leading-5 text-slate-600">
              {refresh?.resetsAt ? `${shortTime(refresh.resetsAt)} · ${refresh.refreshOrderReason || "queued"}` : refresh?.refreshOrderReason || "quota not checked"}
            </div>
          </div>
        </>
      ) : (
        <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
          <div className="min-w-0 rounded-md bg-slate-50 p-2">
            <div className="text-[11px] font-semibold uppercase leading-4 text-slate-400">Quota</div>
            <div className="truncate font-medium text-slate-700">{summary.label}</div>
          </div>
          <div className="min-w-0 rounded-md bg-slate-50 p-2">
            <div className="text-[11px] font-semibold uppercase leading-4 text-slate-400">Dispatch</div>
            <div className="truncate font-medium text-slate-700">{dispatch ? dispatchTarget(dispatch.clientId, dispatch.clientLabel, dispatch.holder) : "None"}</div>
          </div>
        </div>
      )}
    </div>
  );
}

function MetricCard({
  icon,
  label,
  status,
  value,
}: {
  icon: ReactNode;
  label: string;
  status: "success" | "warning" | "danger";
  value: string;
}) {
  return (
    <KPI className="border border-slate-200 bg-white shadow-sm">
      <KPI.Header>
        <KPI.Icon status={status}>{icon}</KPI.Icon>
        <KPI.Title>{label}</KPI.Title>
      </KPI.Header>
      <KPI.Content>
        <KPI.Value value={Number(value)}>{value}</KPI.Value>
      </KPI.Content>
    </KPI>
  );
}

function FieldLabel({ children, text }: { children: ReactNode; text: string }) {
  return (
    <label className="flex min-w-0 flex-col gap-1.5">
      <span className="text-xs font-medium text-slate-600">{text}</span>
      {children}
    </label>
  );
}

function SignalLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="signal-line flex items-center justify-between gap-3 rounded-lg bg-slate-50 p-3">
      <span className="text-slate-500">{label}</span>
      <span className="min-w-0 truncate text-right font-medium text-slate-900">{value}</span>
    </div>
  );
}
