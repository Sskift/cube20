import { useState } from "react";
import { Button, Card, Input } from "@heroui/react";
import { KeyRound, LogIn, ShieldCheck, UserPlus } from "lucide-react";

import { useLang } from "../i18n";
import type { ThemeMode } from "../hooks/useTheme";
import { ThemeToggle } from "./chrome";
import { FieldLabel } from "./primitives";

type AuthTab = "login" | "register" | "token";

// Pre-auth gate, replacing the bare TokenGate. Three tabs share one card frame:
//   - Login    -> POST /api/auth/login    (username + password, sets cookie)
//   - Register -> POST /api/auth/register (username + password, sets cookie)
//   - Token    -> the legacy admin/device-token paste flow (headless bootstrap)
// Login/Register run through useDashboardData (cookie auth); the Token tab keeps
// the App-owned token field so the bearer path still works in parallel.
export function AuthGate({
  busy,
  message,
  onApplyToken,
  onLogin,
  onRegister,
  onThemeToggle,
  onTokenInput,
  themeMode,
  tokenInput,
}: {
  busy: boolean;
  message: string;
  onApplyToken: () => Promise<void> | void;
  onLogin: (username: string, password: string) => Promise<void>;
  onRegister: (username: string, password: string) => Promise<void>;
  onThemeToggle: () => void;
  onTokenInput: (value: string) => void;
  themeMode: ThemeMode;
  tokenInput: string;
}) {
  const { t } = useLang();
  const [tab, setTab] = useState<AuthTab>("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const tabs: { key: AuthTab; label: string }[] = [
    { key: "login", label: t("登录", "Login") },
    { key: "register", label: t("注册", "Register") },
    { key: "token", label: t("令牌", "Token") },
  ];

  const credsReady = username.trim().length > 0 && password.length > 0;

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-xl cube-card cube-elevated">
        <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
          <h1 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <span className="grid h-8 w-8 place-items-center rounded-lg cube-brand">
              <ShieldCheck size={16} />
            </span>
            cube20
          </h1>
          <ThemeToggle mode={themeMode} onToggle={onThemeToggle} />
        </Card.Header>
        <Card.Content className="gap-4">
          <div className="flex gap-1 rounded-xl border border-slate-200 bg-surface p-1">
            {tabs.map((item) => (
              <button
                key={item.key}
                aria-current={tab === item.key ? "page" : undefined}
                className={`flex-1 rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
                  tab === item.key ? "cube-brand shadow-sm" : "text-slate-600 hover:bg-slate-100 hover:text-slate-950"
                }`}
                type="button"
                onClick={() => setTab(item.key)}
              >
                {item.label}
              </button>
            ))}
          </div>

          {tab === "token" ? (
            <>
              <FieldLabel text={t("管理员令牌或设备令牌", "admin token or device token")}>
                <Input
                  fullWidth
                  value={tokenInput}
                  variant="secondary"
                  onChange={(event) => onTokenInput(event.currentTarget.value)}
                />
              </FieldLabel>
              <Button
                className="gap-2"
                isDisabled={busy || !tokenInput.trim()}
                variant="primary"
                onPress={() => onApplyToken()}
              >
                <KeyRound size={15} />
                {t("继续", "Continue")}
              </Button>
            </>
          ) : (
            <>
              <FieldLabel text={t("用户名", "username")}>
                <Input
                  fullWidth
                  autoComplete="username"
                  value={username}
                  variant="secondary"
                  onChange={(event) => setUsername(event.currentTarget.value)}
                />
              </FieldLabel>
              <FieldLabel text={t("密码", "password")}>
                <Input
                  fullWidth
                  autoComplete={tab === "register" ? "new-password" : "current-password"}
                  type="password"
                  value={password}
                  variant="secondary"
                  onChange={(event) => setPassword(event.currentTarget.value)}
                />
              </FieldLabel>
              <Button
                className="gap-2"
                isDisabled={busy || !credsReady}
                variant="primary"
                onPress={async () => {
                  if (tab === "register") await onRegister(username.trim(), password);
                  else await onLogin(username.trim(), password);
                }}
              >
                {tab === "register" ? <UserPlus size={15} /> : <LogIn size={15} />}
                {tab === "register" ? t("注册", "Register") : t("登录", "Login")}
              </Button>
            </>
          )}

          {message && (
            <div className="rounded-lg border border-warning bg-warning-soft p-3 text-sm text-warning-soft-foreground">
              {message}
            </div>
          )}
        </Card.Content>
      </Card>
    </div>
  );
}
