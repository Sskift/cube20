import { Button, Card, Chip, Input } from "@heroui/react";
import { Copy, Gauge, Info, KeyRound, LogOut, RefreshCw, Route, Save, UserRound } from "lucide-react";
import { useLang } from "../i18n";
import { cloudOrigin, cloudToken } from "../api";
import { copyText, maskSecret, shortID, shortTime, tokens } from "../lib/format";
import { AppLayout, CopyLine, FieldLabel, MetricCard, SignalLine } from "../components/primitives";
import { LangToggle, ThemeToggle } from "../components/chrome";
import { DispatchTimeline } from "../components/DispatchTimeline";
import { DevicesView } from "./DevicesView";
import type { ThemeMode } from "../hooks/useTheme";
import type { AccountUsage, PersonalPayload } from "../types";
import type { DashboardData } from "../hooks/useDashboardData";

// Personal (non-admin) client dashboard: profile, access token management, and per-account usage.
export function PersonalDashboard({
  busy,
  data,
  message,
  onApplyToken,
  onClearToken,
  onRefresh,
  onThemeToggle,
  onTokenInput,
  personal,
  themeMode,
  tokenInput,
  usage,
}: {
  busy: boolean;
  data: DashboardData;
  message: string;
  onApplyToken: () => Promise<void>;
  onClearToken: () => Promise<void>;
  onRefresh: () => Promise<void>;
  onThemeToggle: () => void;
  onTokenInput: (value: string) => void;
  personal: PersonalPayload;
  themeMode: ThemeMode;
  tokenInput: string;
  usage: AccountUsage[];
}) {
  const client = personal.client;
  const totals = personal.totals;
  const dispatches = personal.dispatches || [];
  const browserToken = cloudToken();
  const configCommand = `cube cloud config --server ${cloudOrigin()} --token ${browserToken || "<cube_pat_...>"}`;
  const { t } = useLang();

  return (
    <AppLayout
      className="h-screen bg-background"
      navbar={
        <div className="cube-navbar sticky top-0 z-20 flex min-h-14 w-full items-center justify-between gap-3 border-b border-slate-200 cube-glass px-4 py-2">
          <div className="flex min-w-0 items-center gap-3">
            <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg cube-brand">
              <UserRound size={18} />
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-slate-950">{t("我的页面", "My page")}</div>
              <div className="truncate text-xs text-slate-500">{client?.label || client?.id || "client"}</div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <ThemeToggle mode={themeMode} onToggle={onThemeToggle} />
            <LangToggle />
            <Button aria-label="Reload data" className="gap-2" isDisabled={busy} size="sm" variant="secondary" onPress={onRefresh}>
              <RefreshCw size={15} />
              <span className="hidden sm:inline">{t("刷新", "Reload")}</span>
            </Button>
            <Button aria-label="Clear token" className="gap-2" isDisabled={busy} size="sm" variant="danger-soft" onPress={onClearToken}>
              <LogOut size={15} />
              <span className="hidden sm:inline">{t("令牌", "Token")}</span>
            </Button>
          </div>
        </div>
      }
    >
      <div className="cube-content mx-auto flex w-full max-w-6xl flex-col gap-4 p-3 sm:p-4 lg:gap-5 lg:p-6">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
          <MetricCard icon={<UserRound size={18} />} label={t("客户端", "Client")} value={client?.active ? t("活跃", "Active") : t("未活跃", "Inactive")} status={client?.active ? "success" : "danger"} />
          <MetricCard icon={<Gauge size={18} />} label={t("7天 Token 用量", "7d Token Usage")} value={tokens(totals?.sevenDays?.total)} status={(totals?.sevenDays?.total || 0) > 0 ? "success" : "warning"} />
          <MetricCard icon={<Route size={18} />} label={t("调度", "Dispatches")} value={dispatches.length.toString()} status={dispatches.length ? "success" : "warning"} />
        </div>

        <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
          <Card className="cube-card">
            <Card.Header className="border-b border-slate-200 px-5 py-4">
              <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                <UserRound size={17} />
                {t("资料", "Profile")}
              </h2>
            </Card.Header>
            <Card.Content className="gap-3 text-sm">
              <SignalLine label={t("客户端 ID", "client id")} value={client?.id || "-"} />
              <SignalLine label={t("标签", "label")} value={client?.label || "-"} />
              <SignalLine label={t("最近活跃", "last seen")} value={shortTime(client?.lastSeenAt)} />
              <SignalLine label={t("浏览器令牌", "browser token")} value={maskSecret(browserToken)} />
            </Card.Content>
          </Card>

          <Card className="cube-card">
            <Card.Header className="border-b border-slate-200 px-5 py-4">
              <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
                <KeyRound size={17} />
                {t("访问", "Access")}
              </h2>
            </Card.Header>
            <Card.Content className="gap-4">
              <FieldLabel text={t("令牌", "token")}>
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
                  {t("保存令牌", "Save token")}
                </Button>
                <Button className="gap-2" variant="secondary" onPress={() => copyText(configCommand)}>
                  <Copy size={15} />
                  {t("复制配置", "Copy config")}
                </Button>
              </div>
              <CopyLine label={t("本地配置", "Local config")} value={configCommand} />
              <CopyLine label={t("仪表盘", "Dashboard")} value={`${cloudOrigin()}/?token=${browserToken || "<cube_pat_...>"}`} />
            </Card.Content>
          </Card>
        </div>

        {/* Device tokens: non-admin users mint their own per-device tokens here. */}
        <DevicesView data={data} />

        <Card className="cube-card">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Route size={17} />
              {t("调度", "Dispatches")}
            </h2>
            <Chip color={dispatches.length ? "success" : "warning"} variant="soft">
              {dispatches.length}
            </Chip>
          </Card.Header>
          <Card.Content className="gap-2">
            <DispatchTimeline dispatches={dispatches.slice(0, 10)} />
          </Card.Content>
        </Card>

        <Card className="cube-card">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Gauge size={17} />
              {t("按账号用量", "Usage by account")}
            </h2>
            <Chip color="accent" variant="soft">
              {tokens(totals?.allTime?.total)} {t("全部", "all")}
            </Chip>
          </Card.Header>
          <Card.Content className="p-0">
            <div className="divide-y divide-slate-200">
              {usage.map((item) => (
                <div key={item.accountId} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3 text-sm">
                  <div className="min-w-0">
                    <div className="truncate font-semibold text-slate-950">{shortID(item.accountId)}</div>
                    <div className="truncate text-xs text-slate-500">{item.latestModel || item.models?.[0]?.model || t("无模型", "no model")} · {shortTime(item.latestAt || item.updatedAt)}</div>
                  </div>
                  <div className="text-right">
                    <div className="font-semibold text-slate-950">{tokens(item.sevenDays?.total)} 7d</div>
                    <div className="text-xs text-slate-500">{tokens(item.today?.total)} {t("今日", "today")}</div>
                  </div>
                </div>
              ))}
              {!usage.length && <div className="px-4 py-6 text-sm text-slate-500">{t("暂无用量", "No usage yet")}</div>}
            </div>
          </Card.Content>
        </Card>

        {message && (
          <Card className="border border-accent bg-accent-soft text-accent-soft-foreground">
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
