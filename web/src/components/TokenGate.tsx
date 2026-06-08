import { Button, Card, Input } from "@heroui/react";
import { KeyRound, ShieldCheck } from "lucide-react";

import { useLang } from "../i18n";
import type { ThemeMode } from "../hooks/useTheme";
import { ThemeToggle } from "./chrome";
import { FieldLabel } from "./primitives";

// Pre-auth gate: the only screen rendered before a valid admin token or PAT is
// applied. Carries just the theme toggle and a single token field; all state is
// owned by the App shell and passed in.

export function TokenGate({
  busy,
  message,
  onApplyToken,
  onThemeToggle,
  onTokenInput,
  themeMode,
  tokenInput,
}: {
  busy: boolean;
  message: string;
  onApplyToken: () => Promise<void>;
  onThemeToggle: () => void;
  onTokenInput: (value: string) => void;
  themeMode: ThemeMode;
  tokenInput: string;
}) {
  const { t } = useLang();
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
          <FieldLabel text={t("管理员令牌或 PAT", "admin token or PAT")}>
            <Input
              fullWidth
              value={tokenInput}
              variant="secondary"
              onChange={(event) => onTokenInput(event.currentTarget.value)}
            />
          </FieldLabel>
          <Button className="gap-2" isDisabled={busy || !tokenInput.trim()} variant="primary" onPress={onApplyToken}>
            <KeyRound size={15} />
            {t("继续", "Continue")}
          </Button>
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
