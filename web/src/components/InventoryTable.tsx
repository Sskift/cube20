import { useState } from "react";
import { Chip } from "@heroui/react";
import { ChevronDown } from "lucide-react";

import { useLang } from "../i18n";
import { accountStatusColor, shortID, shortTime } from "../lib/format";
import type { Account } from "../types";

// Inventory table for the accounts page. Deliberately NOT the dispatch/pool table
// (that lives on the load-balancer page). This view answers "what accounts do I
// have and how are they configured" — columns are management-oriented: plan,
// pool status, auth/config presence, ownership. Clicking a row selects it (the
// right-hand details panel edits it) and expands the stored file paths. Reuses
// the .cube-table CSS language so it feels consistent without sharing dispatch
// concerns.
export function InventoryTable({
  accounts,
  selectedId,
  onSelect,
}: {
  accounts: Account[];
  selectedId?: string;
  onSelect?: (id: string) => void;
}) {
  const { t } = useLang();
  const [expanded, setExpanded] = useState<string>("");

  if (!accounts.length) {
    return <div className="cube-table-empty">{t("没有账号。", "No accounts.")}</div>;
  }

  function toggle(id: string) {
    setExpanded((current) => (current === id ? "" : id));
    onSelect?.(id);
  }

  return (
    <div className="cube-table inventory" role="table">
      <div className="cube-table-head" role="row">
        <span role="columnheader">{t("账号", "Account")}</span>
        <span role="columnheader">{t("套餐", "Plan")}</span>
        <span role="columnheader">{t("状态", "Status")}</span>
        <span role="columnheader">{t("文件", "Files")}</span>
        <span role="columnheader">{t("归属", "Owner")}</span>
        <span role="columnheader" className="cube-col-chevron" aria-hidden="true" />
      </div>

      {accounts.map((account) => {
        const isOpen = expanded === account.id;
        const isSelected = selectedId === account.id;
        const tone = dotTone(account);
        return (
          <div key={account.id} className={`cube-row-wrap ${isOpen ? "is-open" : ""} ${isSelected ? "is-selected" : ""}`}>
            <button type="button" role="row" aria-expanded={isOpen} className="cube-row" onClick={() => toggle(account.id)}>
              <span className="cube-cell cube-cell-account" role="cell">
                <span className={`cube-dot tone-${tone}`} aria-hidden="true" />
                <span className="min-w-0">
                  <span className="cube-row-name">
                    {account.label || shortID(account.id)}
                    {account.active && <Chip color="accent" size="sm" variant="soft">{t("活跃", "active")}</Chip>}
                  </span>
                  <span className="cube-row-sub">{shortID(account.id)}</span>
                </span>
              </span>

              <span className="cube-cell" role="cell">
                <span className="cube-cell-label">{t("套餐", "Plan")}</span>
                <span className="cube-inv-value">{account.plan || "—"}</span>
              </span>

              <span className="cube-cell" role="cell">
                <span className="cube-cell-label">{t("状态", "Status")}</span>
                <Chip color={accountStatusColor(account.status)} size="sm" variant="soft">{account.status}</Chip>
              </span>

              <span className="cube-cell cube-cell-files" role="cell">
                <span className="cube-cell-label">{t("文件", "Files")}</span>
                <span className="cube-file-badges">
                  <Chip color={account.authPresent ? "success" : "danger"} size="sm" variant="soft">
                    {account.authPresent ? t("凭据", "auth") : t("无凭据", "no auth")}
                  </Chip>
                  <Chip color={account.configPresent ? "success" : "warning"} size="sm" variant="soft">
                    {account.configPresent ? t("配置", "config") : t("无配置", "no config")}
                  </Chip>
                </span>
              </span>

              <span className="cube-cell" role="cell">
                <span className="cube-cell-label">{t("归属", "Owner")}</span>
                <span className="cube-inv-value">
                  {account.ownerMode === "client" ? `client · ${account.ownerClientId || "—"}` : "cloud"}
                </span>
              </span>

              <span className="cube-cell cube-col-chevron" role="cell" aria-hidden="true">
                <ChevronDown className={`cube-chevron ${isOpen ? "is-open" : ""}`} size={16} />
              </span>
            </button>

            {isOpen && <InventoryDetail account={account} t={t} />}
          </div>
        );
      })}
    </div>
  );
}

function InventoryDetail({ account, t }: { account: Account; t: (zh: string, en: string) => string }) {
  return (
    <div className="cube-row-detail">
      <div className="cube-detail-grid">
        <Detail label={t("代次", "generation")} value={String(account.generation || 0)} />
        <Detail label={t("Codex 目录", "codex home")} value={account.codexHome || "—"} />
        <Detail label={t("凭据路径", "auth path")} value={account.authPath || "—"} />
        <Detail label={t("配置路径", "config path")} value={account.configPath || "—"} />
        <Detail label={t("运行状态", "runtime")} value={account.runtimeState || "—"} />
        <Detail label={t("租约类型", "lease kind")} value={account.leaseKind || "—"} />
        <Detail
          label={t("租约", "lease")}
          value={account.leaseActive ? `${account.leaseClientId || account.leaseHolder || "client"} · ${t("至", "until")} ${shortTime(account.leaseExpiresAt)}` : t("空闲", "idle")}
        />
      </div>
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="cube-detail-cell">
      <span className="cube-cell-label">{label}</span>
      <span className="cube-detail-value">{value}</span>
    </div>
  );
}

// Health dot tone — green ready, amber transitional, red disabled / no auth.
function dotTone(account: Account): string {
  if (!account.authPresent || account.status === "disabled") return "danger";
  if (account.status === "recovering" || account.status === "drain") return "warning";
  return "success";
}
