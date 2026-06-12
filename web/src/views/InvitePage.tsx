import { useEffect, useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { Copy, KeyRound, Link2, MonitorSmartphone, ShieldCheck } from "lucide-react";

import { useLang } from "../i18n";
import type { DashboardData } from "../hooks/useDashboardData";
import type { InvitePreview, PersonalPayload } from "../types";
import { LangToggle, ThemeToggle } from "../components/chrome";
import { FieldLabel } from "../components/primitives";

export function InvitePage({
  data,
  themeMode,
  token,
  onThemeToggle,
}: {
  data: DashboardData;
  themeMode: "light" | "dark";
  token: string;
  onThemeToggle: () => void;
}) {
  const { t } = useLang();
  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [error, setError] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [joined, setJoined] = useState<PersonalPayload | null>(null);
  const [deviceLabel, setDeviceLabel] = useState(() => (typeof navigator === "undefined" ? "device" : navigator.platform || "device"));
  const [deviceToken, setDeviceToken] = useState("");
  const user = joined?.user || data.currentUser;
  const workspaces = joined?.workspaces || data.personal?.workspaces || [];
  const joinedWorkspace = workspaces.find((workspace) => workspace.id === preview?.workspaceId);

  useEffect(() => {
    let cancelled = false;
    setError("");
    setPreview(null);
    void data
      .previewInvite(token)
      .then((next) => {
        if (!cancelled) setPreview(next);
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : t("邀请链接不可用", "Invite link unavailable"));
      });
    return () => {
      cancelled = true;
    };
  }, [data, token, t]);

  const copyToken = async () => {
    if (deviceToken && navigator.clipboard?.writeText) await navigator.clipboard.writeText(deviceToken);
  };

  return (
    <div className="min-h-screen bg-background">
      <div className="flex min-h-screen flex-col">
        <header className="flex min-h-14 items-center justify-between border-b border-slate-200 bg-surface px-4">
          <div className="flex items-center gap-3">
            <div className="grid h-9 w-9 place-items-center rounded-lg cube-brand">
              <ShieldCheck size={18} />
            </div>
            <div>
              <div className="text-sm font-semibold text-slate-950">cube20</div>
              <div className="text-xs text-slate-500">{t("工作区邀请", "Workspace invite")}</div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <ThemeToggle mode={themeMode} onToggle={onThemeToggle} />
            <LangToggle />
          </div>
        </header>

        <main className="mx-auto flex w-full max-w-3xl flex-1 flex-col gap-4 px-4 py-8">
          <Card className="cube-card">
            <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
              <div className="flex min-w-0 items-center gap-3">
                <div className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-accent-soft text-accent">
                  <Link2 size={18} />
                </div>
                <div className="min-w-0">
                  <h1 className="truncate text-base font-semibold text-slate-950">
                    {preview?.workspaceName || t("工作区邀请", "Workspace invite")}
                  </h1>
                  <div className="text-xs text-slate-500">{preview?.workspaceId || t("正在加载", "Loading")}</div>
                </div>
              </div>
              {preview && (
                <Chip color="accent" size="sm" variant="soft">
                  {preview.role}
                </Chip>
              )}
            </Card.Header>
            <Card.Content className="space-y-4 p-5">
              {error && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger">{error}</div>}
              {!error && !preview && <div className="text-sm text-slate-500">{t("正在验证邀请链接", "Checking invite link")}</div>}
              {preview && (
                <>
                  {!user && !joined && (
                    <form
                      className="grid gap-3"
                      onSubmit={async (event) => {
                        event.preventDefault();
                        const payload = await data.registerWithInvite(token, username.trim(), password);
                        setJoined(payload);
                      }}
                    >
                      <FieldLabel text={t("用户名", "Username")}>
                        <Input
                          fullWidth
                          autoComplete="username"
                          value={username}
                          variant="secondary"
                          onChange={(event) => setUsername(event.currentTarget.value)}
                        />
                      </FieldLabel>
                      <FieldLabel text={t("密码", "Password")}>
                        <Input
                          fullWidth
                          autoComplete="new-password"
                          type="password"
                          value={password}
                          variant="secondary"
                          onChange={(event) => setPassword(event.currentTarget.value)}
                        />
                      </FieldLabel>
                      <Button isDisabled={data.busy || !username.trim() || password.length < 6} type="submit" variant="primary">
                        {t("注册并加入", "Register and join")}
                      </Button>
                    </form>
                  )}

                  {user && !joinedWorkspace && (
                    <Button
                      isDisabled={data.busy}
                      variant="primary"
                      onPress={async () => {
                        const payload = await data.joinInvite(token);
                        setJoined(payload);
                      }}
                    >
                      {t("加入工作区", "Join workspace")}
                    </Button>
                  )}

                  {(joinedWorkspace || joined) && (
                    <div className="space-y-4">
                      <div className="rounded-md border border-success/30 bg-success-soft px-3 py-2 text-sm text-success">
                        {t("已加入工作区", "Joined workspace")} {preview.workspaceName}
                      </div>
                      <div className="grid gap-3 border-t border-slate-100 pt-4">
                        <div className="flex items-center gap-2 text-sm font-semibold text-slate-950">
                          <MonitorSmartphone size={16} />
                          {t("创建设备", "Create device")}
                        </div>
                        <FieldLabel text={t("设备名称", "Device label")}>
                          <Input
                            fullWidth
                            value={deviceLabel}
                            variant="secondary"
                            onChange={(event) => setDeviceLabel(event.currentTarget.value)}
                          />
                        </FieldLabel>
                        <Button
                          isDisabled={data.busy || !deviceLabel.trim()}
                          variant="secondary"
                          onPress={async () => {
                            const raw = await data.createDevice(deviceLabel.trim());
                            setDeviceToken(raw);
                          }}
                        >
                          <KeyRound size={15} />
                          {t("生成设备 token", "Generate device token")}
                        </Button>
                        {deviceToken && (
                          <div className="grid gap-2 rounded-md border border-slate-200 bg-slate-50 p-3">
                            <div className="break-all font-mono text-xs text-slate-800">{deviceToken}</div>
                            <Button className="w-max gap-1.5" size="sm" variant="primary" onPress={copyToken}>
                              <Copy size={14} />
                              {t("复制 token", "Copy token")}
                            </Button>
                          </div>
                        )}
                      </div>
                    </div>
                  )}
                </>
              )}
            </Card.Content>
          </Card>
        </main>
      </div>
    </div>
  );
}
