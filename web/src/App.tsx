import type { ReactNode } from "react";
import { useEffect, useState } from "react";
import { Button, Card, Chip } from "@heroui/react";
import {
  Database,
  FileJson,
  Gauge,
  Info,
  PanelRightClose,
  PanelRightOpen,
  RefreshCw,
  Route,
  ShieldCheck,
  Users,
} from "lucide-react";

import { useLang } from "./i18n";
import { cloudToken } from "./api";
import { useTheme } from "./hooks/useTheme";
import { useDashboardData } from "./hooks/useDashboardData";
import type { DashboardView } from "./types";
import { AppLayout } from "./components/primitives";
import { LangToggle, NavItem, ThemeToggle } from "./components/chrome";
import { DetailsPanel } from "./components/DetailsPanel";
import { TokenGate } from "./components/TokenGate";
import { AccountsView } from "./views/AccountsView";
import { LoadBalancerView } from "./views/LoadBalancerView";
import { PeopleView } from "./views/PeopleView";
import { ImportView } from "./views/ImportView";
import { PersonalDashboard } from "./views/PersonalDashboard";
import { QuotaOverview } from "./views/QuotaOverview";

// App is the thin shell: it owns only shell-level UI state (active view, the
// responsive sidebar/aside flags, and the token input field) and delegates all
// server state + mutations to useDashboardData. Each view is its own module and
// receives the shared `data` object; the shell just routes between them.
export default function App() {
  const { t } = useLang();
  const [themeMode, toggleTheme] = useTheme();
  const data = useDashboardData(t);
  const [activeView, setActiveView] = useState<DashboardView>("load-balancer");
  const [tokenInput, setTokenInput] = useState(() => cloudToken());
  const [asideOpen, setAsideOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(() => (typeof window === "undefined" ? true : window.innerWidth >= 1180));
  const [compactShell, setCompactShell] = useState(() => (typeof window === "undefined" ? false : window.innerWidth < 1180));

  // Keep the shell layout in sync with the viewport: a roomy desktop gets the
  // sidebar rail, anything narrower collapses to the compact tab strip and never
  // floats the detail aside.
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

  function selectView(view: DashboardView) {
    setActiveView(view);
    if (compactShell) setSidebarOpen(false);
  }

  // Non-admin PAT holders get the personal dashboard instead of the admin shell.
  if (!data.loading && data.accessMode === "personal" && data.personal) {
    return (
      <PersonalDashboard
        busy={data.busy}
        message={data.message}
        personal={data.personal}
        themeMode={themeMode}
        tokenInput={tokenInput}
        usage={data.personalUsage}
        onApplyToken={() => data.applyToken(tokenInput)}
        onClearToken={async () => {
          setTokenInput("");
          await data.clearToken();
        }}
        onRefresh={() => data.loadAll("")}
        onThemeToggle={toggleTheme}
        onTokenInput={setTokenInput}
      />
    );
  }

  // No usable token yet: show the gate before anything else loads.
  if (!data.loading && data.accessMode === "unknown" && !data.accounts.length) {
    return (
      <TokenGate
        busy={data.busy}
        message={data.message}
        themeMode={themeMode}
        tokenInput={tokenInput}
        onApplyToken={() => data.applyToken(tokenInput)}
        onThemeToggle={toggleTheme}
        onTokenInput={setTokenInput}
      />
    );
  }

  // One source of truth for navigation, shared by the desktop rail and the
  // compact tab strip so the two can never drift apart.
  const navItems: { view: DashboardView; icon: ReactNode; label: string; badge?: string }[] = [
    { view: "load-balancer", icon: <Route size={17} />, label: t("负载均衡", "Load Balancer"), badge: data.eligibleCount.toString() },
    { view: "accounts", icon: <Database size={17} />, label: t("账号", "Accounts"), badge: data.accounts.length.toString() },
    { view: "overview", icon: <Gauge size={17} />, label: t("配额总览", "Quota Overview"), badge: data.refreshQueue.length.toString() },
    { view: "people", icon: <Users size={17} />, label: t("成员", "People"), badge: data.activeClientCount.toString() },
    { view: "import", icon: <FileJson size={17} />, label: t("导入凭据", "Import auth") },
  ];
  const navTitle = navItems.find((item) => item.view === activeView)?.label ?? "cube20";

  const sidebar = (
    <div className="flex h-full min-h-0 flex-col border-r border-slate-200 bg-surface">
      <div className="flex items-center gap-3 border-b border-slate-200 px-4 py-4">
        <div className="grid h-10 w-10 place-items-center rounded-xl cube-brand">
          <ShieldCheck size={20} />
        </div>
        <div className="min-w-0">
          <div className="text-base font-semibold text-slate-950">cube20</div>
          <div className="text-xs text-slate-500">{t("Codex 账号池管理", "Codex pool manager")}</div>
        </div>
      </div>
      <div className="flex flex-1 flex-col gap-1 px-3 py-4 text-sm">
        {navItems.map((item) => (
          <NavItem
            key={item.view}
            icon={item.icon}
            label={item.label}
            active={activeView === item.view}
            badge={item.badge}
            onPress={() => selectView(item.view)}
          />
        ))}
      </div>
      <div className="border-t border-slate-200 p-3">
        <div className="rounded-lg bg-slate-50 p-3 text-xs text-slate-600">
          <div className="mb-1 font-medium text-slate-900">{t("本地 Codex", "Live Codex")}</div>
          <div className="path-text font-mono">{data.meta?.liveCodexHome || "-"}</div>
        </div>
      </div>
    </div>
  );

  const compactNav = (
    <nav className="flex gap-1 overflow-x-auto rounded-xl border border-slate-200 bg-surface p-1.5 shadow-sm">
      {navItems.map((item) => {
        const active = activeView === item.view;
        return (
          <button
            key={item.view}
            aria-current={active ? "page" : undefined}
            className={`flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${
              active ? "cube-brand shadow-sm" : "text-slate-600 hover:bg-slate-100 hover:text-slate-950"
            }`}
            type="button"
            onClick={() => selectView(item.view)}
          >
            {item.icon}
            <span>{item.label}</span>
            {item.badge && (
              <span className={`rounded-full px-1.5 text-[10px] ${active ? "bg-white/15 text-white" : "bg-slate-200 text-slate-600"}`}>
                {item.badge}
              </span>
            )}
          </button>
        );
      })}
    </nav>
  );

  const navbar = (
    <div className="cube-navbar sticky top-0 z-20 flex min-h-14 w-full items-center justify-between gap-2 border-b border-slate-200 cube-glass px-3 py-2 sm:gap-3 md:flex-nowrap md:px-4">
      <div className="flex min-w-0 items-center gap-3">
        {compactShell && (
          <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg cube-brand">
            <ShieldCheck size={18} />
          </div>
        )}
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-slate-950">{navTitle}</div>
          <div className="hidden max-w-[min(44vw,34rem)] truncate text-xs text-slate-500 min-[760px]:block">
            {data.meta?.accountsDir || t("正在加载账号目录", "Loading accounts directory")}
          </div>
        </div>
      </div>
      <div className="cube-navbar-actions flex shrink-0 items-center gap-1.5 sm:gap-2">
        <Chip className="hidden min-[900px]:inline-flex" color={data.meta?.liveAuthPresent ? "success" : "warning"} size="sm" variant="soft">
          {t("本地凭据", "live auth")} {data.meta?.liveAuthPresent ? t("就绪", "ready") : t("缺失", "missing")}
        </Chip>
        <ThemeToggle mode={themeMode} onToggle={toggleTheme} />
        <LangToggle />
        <button
          aria-label={asideOpen ? "Hide details" : "Details"}
          className="inline-flex h-8 items-center gap-2 rounded-md border border-slate-200 bg-surface px-2.5 text-sm font-medium text-slate-700 shadow-sm transition-colors hover:bg-slate-50 min-[560px]:px-3"
          type="button"
          onClick={() => setAsideOpen((open) => !open)}
        >
          {asideOpen ? <PanelRightClose size={15} /> : <PanelRightOpen size={15} />}
          <span className="hidden min-[700px]:inline">{t("详情", "Details")}</span>
        </button>
        <Button aria-label="Reload data" className="gap-2" size="sm" variant="secondary" onPress={() => data.loadAll()}>
          <RefreshCw size={15} />
          <span className="hidden min-[700px]:inline">{t("刷新", "Reload")}</span>
        </Button>
      </div>
    </div>
  );

  return (
    <AppLayout
      aside={
        <DetailsPanel
          selected={data.selected}
          busy={data.busy}
          quota={data.selected ? data.quotas[data.selected.id] : undefined}
          refresh={data.selected ? data.refreshByAccount.get(data.selected.id) : undefined}
          dispatch={data.selected ? data.latestDispatchByAccount.get(data.selected.id) : undefined}
          onSave={(draft) => {
            if (data.selected) data.saveAccount(data.selected.id, draft);
          }}
          onDelete={(account) => data.deleteAccount(account)}
        />
      }
      asideOpen={asideOpen}
      className="h-screen bg-background"
      navbar={navbar}
      sidebar={compactShell ? undefined : sidebar}
      sidebarOpen={sidebarOpen}
    >
      <div className="cube-content mx-auto flex w-full max-w-[1500px] flex-col gap-4 p-3 sm:p-4 lg:gap-5 lg:p-6">
        {compactShell && compactNav}

        {activeView === "accounts" && <AccountsView data={data} />}
        {activeView === "load-balancer" && <LoadBalancerView data={data} />}
        {activeView === "overview" && (
          <section className="cube-view-panel">
            <QuotaOverview queue={data.refreshQueue} />
          </section>
        )}
        {activeView === "people" && <PeopleView data={data} />}
        {activeView === "import" && <ImportView data={data} />}

        {data.message && (
          <Card className="border border-accent bg-accent-soft text-accent-soft-foreground">
            <Card.Content className="flex flex-row items-start gap-2 p-4 text-sm">
              <Info size={16} className="mt-0.5 shrink-0" />
              <span>{data.message}</span>
            </Card.Content>
          </Card>
        )}
      </div>
    </AppLayout>
  );
}
