import { useEffect, useState } from "react";
import { Button, Card, Input } from "@heroui/react";
import { Database, Save, Trash2 } from "lucide-react";

import { useLang } from "../i18n";
import { dispatchEventLabel, dispatchTarget, shortTime } from "../lib/format";
import type {
  Account,
  AccountOwnerMode,
  AccountStatus,
  DispatchEvent,
  QuotaResult,
  RefreshQueueItem,
} from "../types";
import { EmptyState, FieldLabel, NativeSelect, SignalLine } from "./primitives";

// Detail panel for the currently opened account. Self-contained: it keeps its
// own draft form state (label/status/owner) synced to the selected account, and
// reports edits up through onSave/onDelete rather than reaching into App state.

export function DetailsPanel({
  selected,
  busy,
  quota,
  refresh,
  dispatch,
  onSave,
  onDelete,
}: {
  selected?: Account;
  busy: boolean;
  quota?: QuotaResult;
  refresh?: RefreshQueueItem;
  dispatch?: DispatchEvent;
  onSave: (draft: { label: string; status: AccountStatus; ownerMode: AccountOwnerMode; ownerClientId?: string }) => void;
  onDelete: (account: Account) => void;
}) {
  const { t } = useLang();
  const [label, setLabel] = useState(selected?.label || "");
  const [status, setStatus] = useState<AccountStatus>(selected?.status || "ready");
  const [ownerMode, setOwnerMode] = useState<AccountOwnerMode>(selected?.ownerMode || "cloud");

  useEffect(() => {
    if (!selected) return;
    setLabel(selected.label || "");
    setStatus(selected.status);
    setOwnerMode(selected.ownerMode || "cloud");
  }, [selected]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-surface">
      <div className="border-b border-slate-200 px-5 py-4">
        <div className="text-sm font-semibold text-slate-950">{t("账号详情", "Account detail")}</div>
        <div className="mt-1 text-xs text-slate-500">{selected ? `${selected.status} · ${selected.authPresent ? t("凭据就绪", "auth ready") : t("缺少凭据", "auth missing")}` : t("未打开账号", "No account opened")}</div>
      </div>
      <div className="flex-1 space-y-4 overflow-auto p-5">
        {selected ? (
          <>
            <Card className="border border-slate-200 shadow-none">
              <Card.Content className="gap-4">
                <FieldLabel text={t("昵称", "Nickname")}>
                  <Input
                    fullWidth
                    value={label}
                    variant="secondary"
                    onChange={(event) => setLabel(event.currentTarget.value)}
                  />
                </FieldLabel>
                <FieldLabel text={t("池状态", "Pool status")}>
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
                <FieldLabel text={t("归属", "Owner")}>
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
                <Button className="gap-2" isDisabled={busy} variant="secondary" onPress={() => onSave({ label, status, ownerMode, ownerClientId: selected.ownerClientId || "" })}>
                  <Save size={15} />
                  {t("保存", "Save")}
                </Button>
                <Button className="gap-2" isDisabled={busy} variant="danger-soft" onPress={() => onDelete(selected)}>
                  <Trash2 size={15} />
                  {t("删除账号", "Delete account")}
                </Button>
              </Card.Content>
            </Card>

            <Card className="border border-slate-200 shadow-none">
              <Card.Header className="border-b border-slate-100 px-4 py-3 text-sm font-semibold">{t("云端信号", "Cloud signals")}</Card.Header>
              <Card.Content className="gap-3 text-xs">
                <SignalLine label={t("5h 配额", "5h quota")} value={refresh?.remainingDisplay ? `${refresh.remainingDisplay} ${t("剩余", "left")}` : refresh?.refreshOrderReason || "-"} />
                <SignalLine label={t("5h 刷新", "5h reset")} value={refresh?.resetsAt ? shortTime(refresh.resetsAt) : refresh?.refreshOrderReason || "-"} />
                <SignalLine label={t("7d 配额", "7d quota")} value={refresh?.sevenDayRemainingDisplay ? `${refresh.sevenDayRemainingDisplay} ${t("剩余", "left")}` : "-"} />
                <SignalLine label={t("7d 刷新", "7d reset")} value={refresh?.sevenDayResetsAt ? shortTime(refresh.sevenDayResetsAt) : "-"} />
                <SignalLine label={t("配额来源", "quota source")} value={refresh?.quotaSource ? `${refresh.quotaSource}${refresh.quotaReporterClientId ? ` · ${refresh.quotaReporterClientId}` : ""}` : quota?.source || "-"} />
                <SignalLine label={t("代次", "generation")} value={(selected.generation || 0).toString()} />
                <SignalLine label={t("归属", "owner")} value={selected.ownerMode === "client" ? `client ${selected.ownerClientId || "-"}` : "cloud"} />
                <SignalLine label={t("租约", "lease")} value={selected.leaseActive ? `${selected.leaseClientId || selected.leaseHolder || "client"} ${t("至", "until")} ${shortTime(selected.leaseExpiresAt)}` : "-"} />
              </Card.Content>
            </Card>

            <Card className="border border-slate-200 shadow-none">
              <Card.Header className="border-b border-slate-100 px-4 py-3 text-sm font-semibold">{t("调度", "Dispatch")}</Card.Header>
              <Card.Content className="gap-3 text-xs">
                <SignalLine label={t("当前租约", "current lease")} value={selected.leaseActive ? dispatchTarget(selected.leaseClientId, "", selected.leaseHolder) : "-"} />
                <SignalLine label={t("租约到期", "lease expires")} value={selected.leaseActive ? shortTime(selected.leaseExpiresAt) : "-"} />
                <SignalLine label={t("最近调度", "last dispatch")} value={dispatch ? `${dispatchEventLabel(dispatch.event, t)} · ${shortTime(dispatch.createdAt)}` : "-"} />
                <SignalLine label={t("分配给", "sent to")} value={dispatch ? dispatchTarget(dispatch.clientId, dispatch.clientLabel, dispatch.holder) : "-"} />
              </Card.Content>
            </Card>
          </>
        ) : (
          <EmptyState size="md" className="py-8">
            <EmptyState.Media variant="icon">
              <Database size={24} />
            </EmptyState.Media>
            <EmptyState.Title>{t("打开一个账号", "Open an account")}</EmptyState.Title>
            <EmptyState.Description>{t("用账号网格查看凭据文件与路由状态。", "Use the account grid to inspect auth files and route status.")}</EmptyState.Description>
          </EmptyState>
        )}
      </div>
    </div>
  );
}
