import { Card } from "@heroui/react";
import { FileJson, UploadCloud } from "lucide-react";

import { useLang } from "../i18n";
import { DropZone } from "../components/primitives";
import type { DashboardData } from "../hooks/useDashboardData";

// The import page: a single drop zone that accepts a raw Codex auth.json or a
// cube20 profile JSON and hands it to the shared upload action.
export function ImportView({ data }: { data: DashboardData }) {
  const { t } = useLang();

  return (
    <section className="cube-view-panel">
      <Card className="cube-card">
        <Card.Header className="border-b border-slate-200 px-5 py-4">
          <h2 className="flex items-center gap-2 text-base font-semibold text-slate-950">
            <UploadCloud size={17} />
            {t("导入 auth.json", "Import auth.json")}
          </h2>
        </Card.Header>
        <Card.Content>
          <DropZone>
            <DropZone.Area className="min-h-32 rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-5 text-center">
              <DropZone.Icon>
                <FileJson size={26} />
              </DropZone.Icon>
              <DropZone.Label>{t("拖入或选择 auth.json", "Drop or choose auth.json")}</DropZone.Label>
              <DropZone.Description>{t("原始 Codex auth.json 或 cube20 配置 JSON", "Raw Codex auth.json or cube20 profile JSON")}</DropZone.Description>
              <DropZone.Input accept=".json,application/json" onSelect={data.uploadFiles} />
            </DropZone.Area>
          </DropZone>
        </Card.Content>
      </Card>
    </section>
  );
}
