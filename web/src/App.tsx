import type { ChangeEvent, FormEvent } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Chip,
  Divider,
  Input,
  Select,
  SelectItem,
  Spinner,
  Textarea,
} from "@heroui/react";
import {
  CheckCircle,
  Database,
  FolderOpen,
  Gauge,
  Play,
  RefreshCw,
  RotateCcw,
  Save,
  ShieldCheck,
  Trash2,
  Upload,
} from "lucide-react";

type AccountStatus = "ready" | "drain" | "disabled";

interface Account {
  id: string;
  label: string;
  plan: string;
  status: AccountStatus;
  codexHome: string;
  authPresent: boolean;
  configPresent: boolean;
  active: boolean;
}

interface Meta {
  statePath: string;
  settingsPath: string;
  accountsDir: string;
  liveCodexHome: string;
  liveAuthPresent: boolean;
  liveConfigPresent: boolean;
}

interface SettingsPayload {
  settingsPath: string;
  settingsToml: string;
  liveCodexHome: string;
  accountsDir: string;
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
  detail?: string;
  quotas?: QuotaItem[];
}

interface UsageToken {
  total: number;
  input: number;
  cachedInput: number;
  output: number;
}

interface UsageSummary {
  status: string;
  filesScanned: number;
  events: number;
  today: UsageToken;
  sevenDays: UsageToken;
}

interface LoadBalanceAccount {
  id: string;
  label: string;
  status: AccountStatus;
  authPresent: boolean;
  configPresent: boolean;
  active: boolean;
  codexHome: string;
  eligible: boolean;
  reason?: string;
}

interface LoadBalanceStatus {
  policy: string;
  statePath: string;
  lastAccountId: string;
  eligible: LoadBalanceAccount[];
  excluded: LoadBalanceAccount[];
}

async function apiJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (!headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, { ...init, headers });
  const text = await response.text();
  const data = text ? JSON.parse(text) : {};
  if (!response.ok) {
    throw new Error(data.error || response.statusText);
  }
  return data as T;
}

function shortID(id: string) {
  return id.length > 14 ? `${id.slice(0, 10)}...` : id;
}

function tokens(value?: number) {
  if (!value) return "0";
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return Math.round(value).toString();
}

function quotaLabel(quota?: QuotaResult) {
  if (!quota) return "not checked";
  if (quota.status === "loading") return "checking";
  if (quota.status === "supported" && quota.quotas?.length) {
    const first = quota.quotas[0];
    return `${first.remainingDisplay} left`;
  }
  return quota.status;
}

export default function App() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [lb, setLB] = useState<LoadBalanceStatus | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [label, setLabel] = useState("");
  const [status, setStatus] = useState<AccountStatus>("ready");
  const [liveHome, setLiveHome] = useState("");
  const [accountsDir, setAccountsDir] = useState("");
  const [settingsToml, setSettingsToml] = useState("");
  const [quotas, setQuotas] = useState<Record<string, QuotaResult>>({});
  const [usages, setUsages] = useState<Record<string, UsageSummary>>({});
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const selected = useMemo(
    () => accounts.find((account) => account.id === selectedId),
    [accounts, selectedId],
  );
  const readyCount = accounts.filter((account) => account.status === "ready").length;
  const eligibleCount = lb?.eligible.length ?? 0;

  async function loadAll(preferredId = selectedId) {
    setLoading(true);
    try {
      const [metaData, accountData, lbData, settingsData] = await Promise.all([
        apiJSON<Meta>("/api/meta"),
        apiJSON<Account[]>("/api/accounts"),
        apiJSON<LoadBalanceStatus>("/api/lb/status"),
        apiJSON<SettingsPayload>("/api/settings"),
      ]);
      setMeta(metaData);
      setAccounts(accountData);
      setLB(lbData);
      setLiveHome(settingsData.liveCodexHome);
      setAccountsDir(settingsData.accountsDir);
      setSettingsToml(settingsData.settingsToml);
      if (!accountData.some((account) => account.id === preferredId)) {
        setSelectedId(accountData.find((account) => account.active)?.id || accountData[0]?.id || "");
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "load failed");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadAll("");
  }, []);

  useEffect(() => {
    if (!selected) return;
    setLabel(selected.label || "");
    setStatus(selected.status);
  }, [selected]);

  async function withBusy(action: () => Promise<void>) {
    setBusy(true);
    try {
      await action();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "action failed");
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
      setMessage("Account saved");
      await loadAll(selected.id);
    });
  }

  async function activateAccount() {
    if (!selected) return;
    await withBusy(async () => {
      await apiJSON(`/api/accounts/${encodeURIComponent(selected.id)}/activate`, { method: "POST" });
      setMessage("auth.json and config.toml activated");
      await loadAll(selected.id);
    });
  }

  async function deleteAccount() {
    if (!selected) return;
    const name = selected.label || selected.id;
    if (!window.confirm(`Delete ${name}? This removes the managed Codex home snapshot.`)) return;
    await withBusy(async () => {
      await apiJSON(`/api/accounts/${encodeURIComponent(selected.id)}`, { method: "DELETE" });
      setMessage("Account deleted");
      setSelectedId("");
      await loadAll("");
    });
  }

  async function importLive() {
    await withBusy(async () => {
      const account = await apiJSON<Account>("/api/accounts/import-live", { method: "POST" });
      setMessage(`Imported ${account.label || account.id}`);
      await loadAll(account.id);
      setSelectedId(account.id);
    });
  }

  async function uploadJSON(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) return;
    await withBusy(async () => {
      const text = await file.text();
      const account = await apiJSON<Account>("/api/accounts/import-json", {
        method: "POST",
        body: text,
      });
      setMessage(`Imported ${account.label || account.id}`);
      setSelectedId(account.id);
      await loadAll(account.id);
    });
    if (fileInput.current) fileInput.current.value = "";
  }

  async function fetchQuota(id: string, quiet = false) {
    setQuotas((current) => ({ ...current, [id]: { status: "loading" } }));
    try {
      const result = await apiJSON<QuotaResult>(`/api/accounts/${encodeURIComponent(id)}/quota`);
      setQuotas((current) => ({ ...current, [id]: result }));
      if (!quiet) setMessage("Quota refreshed");
    } catch (error) {
      const detail = error instanceof Error ? error.message : "quota failed";
      setQuotas((current) => ({ ...current, [id]: { status: "error", detail } }));
      if (!quiet) setMessage(detail);
    }
  }

  async function fetchUsage(id: string) {
    await withBusy(async () => {
      const result = await apiJSON<UsageSummary>(`/api/accounts/${encodeURIComponent(id)}/usage`);
      setUsages((current) => ({ ...current, [id]: result }));
      setMessage("Usage refreshed");
    });
  }

  async function refreshAllQuotas() {
    await withBusy(async () => {
      for (const account of accounts) {
        await fetchQuota(account.id, true);
      }
      setMessage("All quotas refreshed");
    });
  }

  async function savePathSettings(event: FormEvent) {
    event.preventDefault();
    await withBusy(async () => {
      await apiJSON<Meta>("/api/settings", {
        method: "PATCH",
        body: JSON.stringify({ liveCodexHome: liveHome, accountsDir }),
      });
      setMessage("Settings paths saved");
      await loadAll(selectedId);
    });
  }

  async function saveRawSettings() {
    await withBusy(async () => {
      const settings = await apiJSON<SettingsPayload>("/api/settings", {
        method: "PUT",
        body: JSON.stringify({ settingsToml }),
      });
      setLiveHome(settings.liveCodexHome);
      setAccountsDir(settings.accountsDir);
      setSettingsToml(settings.settingsToml);
      setMessage("settings.toml saved");
      await loadAll(selectedId);
    });
  }

  async function pickNext() {
    await withBusy(async () => {
      const account = await apiJSON<Account>("/api/lb/pick", { method: "POST" });
      setSelectedId(account.id);
      setMessage(`Selected ${account.label || account.id}`);
      await loadAll(account.id);
    });
  }

  async function resetLB() {
    await withBusy(async () => {
      const status = await apiJSON<LoadBalanceStatus>("/api/lb/reset", { method: "POST" });
      setLB(status);
      setMessage("Round-robin state reset");
    });
  }

  return (
    <div className="min-h-screen bg-[#f5f7fb] text-[#182230]">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex min-h-16 max-w-[1440px] items-center justify-between gap-4 px-5 lg:px-8">
          <div className="flex items-center gap-3">
            <div className="grid h-10 w-10 place-items-center rounded-lg bg-teal-600 text-white">
              <ShieldCheck size={20} />
            </div>
            <div>
              <div className="text-lg font-semibold tracking-normal">cube20</div>
              <div className="text-xs text-slate-500">Codex account pool</div>
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Chip color={meta?.liveAuthPresent ? "success" : "warning"} variant="flat">
              live auth {meta?.liveAuthPresent ? "ready" : "missing"}
            </Chip>
            <Button size="sm" variant="flat" startContent={<RefreshCw size={15} />} onPress={() => loadAll()}>
              Refresh
            </Button>
            <Button size="sm" color="primary" startContent={<Gauge size={15} />} onPress={refreshAllQuotas}>
              Check Quotas
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto grid max-w-[1440px] grid-cols-1 gap-5 px-5 py-6 lg:grid-cols-[minmax(0,1fr)_420px] lg:px-8">
        <section className="flex flex-col gap-5">
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Metric label="Accounts" value={accounts.length.toString()} accent="bg-teal-50 text-teal-700" />
            <Metric label="Ready" value={readyCount.toString()} accent="bg-emerald-50 text-emerald-700" />
            <Metric label="LB Pool" value={eligibleCount.toString()} accent="bg-amber-50 text-amber-700" />
            <Metric label="Active" value={accounts.find((account) => account.active)?.label || "-"} accent="bg-indigo-50 text-indigo-700" />
          </div>

          <Card radius="sm" shadow="sm" className="border border-slate-200 bg-white">
            <CardHeader className="flex items-center justify-between gap-3 border-b border-slate-100 px-5 py-4">
              <div>
                <h1 className="text-xl font-semibold tracking-normal">Accounts</h1>
                <p className="max-w-[720px] truncate text-xs text-slate-500">{meta?.accountsDir || "loading"}</p>
              </div>
              <Button color="primary" startContent={<CheckCircle size={16} />} isDisabled={!selected || busy} onPress={activateAccount}>
                Activate
              </Button>
            </CardHeader>
            <CardBody className="p-0">
              {loading ? (
                <div className="grid min-h-64 place-items-center">
                  <Spinner />
                </div>
              ) : accounts.length === 0 ? (
                <div className="p-8 text-sm text-slate-500">No accounts imported.</div>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[880px] border-collapse text-left text-sm">
                    <thead className="bg-slate-50 text-xs uppercase text-slate-500">
                      <tr>
                        <th className="px-5 py-3 font-semibold">Nickname</th>
                        <th className="px-5 py-3 font-semibold">Status</th>
                        <th className="px-5 py-3 font-semibold">Files</th>
                        <th className="px-5 py-3 font-semibold">Quota</th>
                        <th className="px-5 py-3 font-semibold">Local Usage</th>
                        <th className="px-5 py-3 font-semibold">Home</th>
                      </tr>
                    </thead>
                    <tbody>
                      {accounts.map((account) => {
                        const quota = quotas[account.id];
                        const usage = usages[account.id];
                        const rowSelected = account.id === selectedId;
                        return (
                          <tr
                            key={account.id}
                            className={`cursor-pointer border-t border-slate-100 ${rowSelected ? "bg-teal-50/70" : "hover:bg-slate-50"}`}
                            onClick={() => setSelectedId(account.id)}
                          >
                            <td className="px-5 py-4">
                              <div className="flex items-center gap-2">
                                <div>
                                  <div className="font-medium text-slate-950">{account.label || account.id}</div>
                                  <div className="font-mono text-xs text-slate-500" title={account.id}>{shortID(account.id)}</div>
                                </div>
                                {account.active && <Chip size="sm" color="primary" variant="flat">active</Chip>}
                              </div>
                            </td>
                            <td className="px-5 py-4">
                              <Chip
                                size="sm"
                                color={account.status === "ready" ? "success" : account.status === "drain" ? "warning" : "danger"}
                                variant="flat"
                              >
                                {account.status}
                              </Chip>
                            </td>
                            <td className="px-5 py-4">
                              <div className="flex gap-1">
                                <Chip size="sm" color={account.authPresent ? "success" : "danger"} variant="flat">auth</Chip>
                                <Chip size="sm" color={account.configPresent ? "default" : "warning"} variant="flat">settings</Chip>
                              </div>
                            </td>
                            <td className="px-5 py-4">
                              <div className="flex items-center gap-2">
                                <span className="text-xs text-slate-600">{quotaLabel(quota)}</span>
                                <Button size="sm" variant="light" onPress={() => fetchQuota(account.id, false)}>Quota</Button>
                              </div>
                              {quota?.detail && <div className="mt-1 max-w-[220px] truncate text-xs text-rose-600">{quota.detail}</div>}
                            </td>
                            <td className="px-5 py-4">
                              {usage ? (
                                <div className="text-xs text-slate-600">
                                  <div className="font-medium text-slate-900">{tokens(usage.today?.total)} today</div>
                                  <div>{tokens(usage.sevenDays?.total)} 7d</div>
                                </div>
                              ) : (
                                <Button size="sm" variant="light" onPress={() => fetchUsage(account.id)}>Usage</Button>
                              )}
                            </td>
                            <td className="max-w-[260px] truncate px-5 py-4 font-mono text-xs text-slate-500" title={account.codexHome}>
                              {account.codexHome}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </CardBody>
          </Card>
        </section>

        <aside className="flex flex-col gap-5">
          <Card radius="sm" shadow="sm" className="border border-slate-200 bg-white">
            <CardHeader className="border-b border-slate-100 px-5 py-4">
              <h2 className="text-base font-semibold">Selected Account</h2>
            </CardHeader>
            <CardBody className="gap-4 p-5">
              {selected ? (
                <>
                  <Input label="Nickname" value={label} onValueChange={setLabel} />
                  <Select
                    label="Pool Status"
                    selectedKeys={[status]}
                    onSelectionChange={(keys) => {
                      const key = Array.from(keys)[0]?.toString() as AccountStatus | undefined;
                      if (key) setStatus(key);
                    }}
                  >
                    <SelectItem key="ready">ready</SelectItem>
                    <SelectItem key="drain">drain</SelectItem>
                    <SelectItem key="disabled">disabled</SelectItem>
                  </Select>
                  <div className="grid grid-cols-2 gap-2">
                    <Button variant="flat" startContent={<Save size={15} />} isDisabled={busy} onPress={saveAccount}>Save</Button>
                    <Button color="primary" startContent={<CheckCircle size={15} />} isDisabled={busy} onPress={activateAccount}>Switch</Button>
                  </div>
                  <Button color="danger" variant="flat" startContent={<Trash2 size={15} />} isDisabled={busy} onPress={deleteAccount}>
                    Delete Account
                  </Button>
                  <div className="rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-xs text-slate-600">
                    {selected.codexHome}
                  </div>
                </>
              ) : (
                <div className="text-sm text-slate-500">Select an account.</div>
              )}
            </CardBody>
          </Card>

          <Card radius="sm" shadow="sm" className="border border-slate-200 bg-white">
            <CardHeader className="flex items-center justify-between border-b border-slate-100 px-5 py-4">
              <h2 className="text-base font-semibold">Load Balancer</h2>
              <Chip color="success" variant="flat">{eligibleCount} ready</Chip>
            </CardHeader>
            <CardBody className="gap-3 p-5">
              <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
                last: {lb?.lastAccountId || "-"}
              </div>
              <div className="grid grid-cols-2 gap-2">
                <Button color="primary" variant="flat" startContent={<Play size={15} />} onPress={pickNext}>Pick Next</Button>
                <Button variant="flat" startContent={<RotateCcw size={15} />} onPress={resetLB}>Reset</Button>
              </div>
              {lb?.excluded.length ? (
                <div className="space-y-1 text-xs text-slate-500">
                  {lb.excluded.slice(0, 4).map((account) => (
                    <div key={account.id} className="flex justify-between gap-2">
                      <span>{account.label || shortID(account.id)}</span>
                      <span>{account.reason}</span>
                    </div>
                  ))}
                </div>
              ) : null}
            </CardBody>
          </Card>

          <Card radius="sm" shadow="sm" className="border border-slate-200 bg-white">
            <CardHeader className="border-b border-slate-100 px-5 py-4">
              <h2 className="flex items-center gap-2 text-base font-semibold"><Upload size={17} /> Import</h2>
            </CardHeader>
            <CardBody className="gap-3 p-5">
              <Button variant="flat" startContent={<Database size={15} />} isDisabled={busy} onPress={importLive}>
                Import Current Codex
              </Button>
              <label className="flex h-12 cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed border-slate-300 bg-slate-50 px-3 text-sm font-medium text-slate-700 hover:border-teal-500 hover:bg-teal-50">
                <Upload size={15} />
                Upload auth.json
                <input ref={fileInput} type="file" accept=".json,application/json" className="hidden" onChange={uploadJSON} />
              </label>
            </CardBody>
          </Card>

          <Card radius="sm" shadow="sm" className="border border-slate-200 bg-white">
            <CardHeader className="border-b border-slate-100 px-5 py-4">
              <h2 className="flex items-center gap-2 text-base font-semibold"><FolderOpen size={17} /> settings.toml</h2>
            </CardHeader>
            <CardBody className="gap-4 p-5">
              <form className="flex flex-col gap-3" onSubmit={savePathSettings}>
                <Input label="live_codex_home" value={liveHome} onValueChange={setLiveHome} />
                <Input label="accounts_dir" value={accountsDir} onValueChange={setAccountsDir} />
                <Button type="submit" color="primary" variant="flat" isDisabled={busy}>Save Paths</Button>
              </form>
              <Divider />
              <Textarea minRows={5} value={settingsToml} onValueChange={setSettingsToml} className="font-mono" />
              <Button variant="flat" startContent={<Save size={15} />} isDisabled={busy} onPress={saveRawSettings}>Save TOML</Button>
              <div className="break-all font-mono text-xs text-slate-500">{meta?.settingsPath}</div>
            </CardBody>
          </Card>

          {message && (
            <Card radius="sm" className="border border-teal-200 bg-teal-50 text-teal-900">
              <CardBody className="p-4 text-sm">{message}</CardBody>
            </Card>
          )}
        </aside>
      </main>
    </div>
  );
}

function Metric({ label, value, accent }: { label: string; value: string; accent: string }) {
  return (
    <Card radius="sm" shadow="sm" className="border border-slate-200 bg-white">
      <CardBody className="flex flex-row items-center justify-between gap-3 p-4">
        <div>
          <div className="text-xs font-medium uppercase text-slate-500">{label}</div>
          <div className="max-w-[150px] truncate text-2xl font-semibold tracking-normal text-slate-950">{value}</div>
        </div>
        <div className={`h-9 w-9 rounded-lg ${accent}`} />
      </CardBody>
    </Card>
  );
}
