import { useEffect, useState } from "react";
import { Button, Card, Chip, Input } from "@heroui/react";
import { MonitorSmartphone, Plus, Trash2 } from "lucide-react";

import { useLang } from "../i18n";
import { cloudOrigin } from "../api";
import { CopyLine, FieldLabel } from "../components/primitives";
import { shortTime } from "../lib/format";
import type { DashboardData } from "../hooks/useDashboardData";

// DevicesView lists the current user's device tokens and mints new ones. The raw
// cube_dev_ token is shown ONCE in a copyable panel right after creation (modeled
// on the createdClientToken reveal in PeopleView). Reachable from both the admin
// shell nav and the PersonalDashboard, so non-admin users can mint devices too.
export function DevicesView({ data }: { data: DashboardData }) {
  const { t } = useLang();
  const { devices, busy } = data;
  const [label, setLabel] = useState("");
  // The one-time token + the label it was minted under, held only in this view.
  const [revealToken, setRevealToken] = useState("");
  const [revealLabel, setRevealLabel] = useState("");

  // Refresh the device list when the view mounts so it is current regardless of
  // which shell rendered it.
  useEffect(() => {
    void data.listDevices().catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const configCommand = `cube device config --server ${cloudOrigin()} --token ${revealToken} --label ${revealLabel || "<label>"}`;

  return (
    <section className="cube-view-panel">
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
        <Card className="cube-card">
          <Card.Header className="border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <Plus size={17} />
              {t("新增设备", "Add device")}
            </h2>
          </Card.Header>
          <Card.Content className="gap-4">
            <FieldLabel text={t("设备标签", "device label")}>
              <Input
                fullWidth
                placeholder="laptop-codex"
                value={label}
                variant="secondary"
                onChange={(event) => setLabel(event.currentTarget.value)}
              />
            </FieldLabel>
            <Button
              className="gap-2"
              isDisabled={busy || !label.trim()}
              variant="primary"
              onPress={async () => {
                try {
                  const token = await data.createDevice(label.trim());
                  setRevealToken(token);
                  setRevealLabel(label.trim());
                  setLabel("");
                } catch {
                  // surfaced via the shared message banner
                }
              }}
            >
              <Plus size={15} />
              {t("创建设备", "Create device")}
            </Button>
            {revealToken && (
              <div className="rounded-lg border border-success bg-success-soft p-3">
                <div className="mb-2 text-xs font-semibold uppercase text-success-soft-foreground">
                  {t("设备令牌(仅显示一次)", "Device token (shown once)")}
                </div>
                <div className="path-text font-mono text-xs text-slate-950">{revealToken}</div>
                <div className="mt-3 grid grid-cols-1 gap-2">
                  <CopyLine label={t("设备配置", "Device config")} value={configCommand} />
                </div>
              </div>
            )}
          </Card.Content>
        </Card>

        <Card className="cube-card">
          <Card.Header className="flex items-center justify-between gap-3 border-b border-slate-200 px-5 py-4">
            <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
              <MonitorSmartphone size={17} />
              {t("我的设备", "My devices")}
            </h2>
            <Chip color={data.activeDeviceCount > 0 ? "success" : "warning"} variant="soft">
              {data.activeDeviceCount}/{devices.length}
            </Chip>
          </Card.Header>
          <Card.Content className="p-0">
            <div className="divide-y divide-slate-200">
              {devices.map((device) => (
                <div key={device.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 py-3">
                  <div className="min-w-0">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate text-sm font-semibold text-slate-950">{device.label || device.id}</span>
                      <Chip color={device.active ? "success" : "danger"} size="sm" variant="soft">
                        {device.active ? t("活跃", "active") : t("已吊销", "revoked")}
                      </Chip>
                    </div>
                    <div className="mt-1 font-mono text-xs text-slate-500">{device.id}</div>
                    <div className="mt-1 text-xs text-slate-500">
                      {t("最近活跃", "last seen")} {shortTime(device.lastSeenAt)}
                    </div>
                  </div>
                  <Button
                    aria-label={`Revoke ${device.id}`}
                    className="gap-2"
                    isDisabled={busy || !device.active}
                    size="sm"
                    variant="danger-soft"
                    onPress={() => data.revokeDevice(device.id)}
                  >
                    <Trash2 size={14} />
                    {t("吊销", "Revoke")}
                  </Button>
                </div>
              ))}
              {!devices.length && <div className="px-4 py-6 text-sm text-slate-500">{t("暂无设备", "No devices")}</div>}
            </div>
          </Card.Content>
        </Card>
      </div>
    </section>
  );
}
