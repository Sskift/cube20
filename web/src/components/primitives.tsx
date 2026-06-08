import type { ChangeEvent, ReactNode } from "react";
import { Button } from "@heroui/react";
import { Copy } from "lucide-react";

import { clampUIPercent, copyText } from "../lib/format";

// Shared presentational primitives. These are pure layout/display building
// blocks with no data dependencies — every view composes them.

export function AppLayout({
  aside,
  asideOpen,
  children,
  className = "",
  navbar,
  sidebar,
  sidebarOpen,
}: {
  aside?: ReactNode;
  asideOpen?: boolean;
  children: ReactNode;
  className?: string;
  navbar?: ReactNode;
  sidebar?: ReactNode;
  sidebarOpen?: boolean;
}) {
  return (
    <div className={`flex min-h-0 overflow-hidden ${className}`}>
      {sidebar && sidebarOpen !== false && <aside className="hidden w-[17rem] shrink-0 lg:block">{sidebar}</aside>}
      <div className="flex min-w-0 flex-1 flex-col">
        {navbar}
        <main className="min-h-0 flex-1 overflow-auto">{children}</main>
      </div>
      {aside && asideOpen && <aside className="hidden w-[24rem] shrink-0 border-l border-slate-200 xl:block">{aside}</aside>}
    </div>
  );
}

export const DropZone = Object.assign(
  function DropZoneRoot({ children }: { children: ReactNode }) {
    return <div>{children}</div>;
  },
  {
    Area({ children, className = "" }: { children: ReactNode; className?: string }) {
      return <div className={className}>{children}</div>;
    },
    Icon({ children }: { children: ReactNode }) {
      return <div className="mb-2 flex justify-center text-slate-500">{children}</div>;
    },
    Label({ children }: { children: ReactNode }) {
      return <div className="text-sm font-semibold text-slate-900">{children}</div>;
    },
    Description({ children }: { children: ReactNode }) {
      return <div className="mt-1 text-xs text-slate-500">{children}</div>;
    },
    Input({ accept, onSelect }: { accept?: string; onSelect: (files: FileList) => void }) {
      return (
        <input
          accept={accept}
          className="mt-3 text-sm text-slate-600"
          type="file"
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            if (event.currentTarget.files) onSelect(event.currentTarget.files);
          }}
        />
      );
    },
  },
);

export const EmptyState = Object.assign(
  function EmptyStateRoot({ children, className = "" }: { children: ReactNode; className?: string; size?: string }) {
    return <div className={`flex flex-col items-center justify-center text-center ${className}`}>{children}</div>;
  },
  {
    Media({ children }: { children: ReactNode; variant?: string }) {
      return <div className="mb-3 grid h-12 w-12 place-items-center rounded-lg bg-slate-100 text-slate-500">{children}</div>;
    },
    Title({ children }: { children: ReactNode }) {
      return <div className="text-sm font-semibold text-slate-950">{children}</div>;
    },
    Description({ children }: { children: ReactNode }) {
      return <div className="mt-1 max-w-sm text-xs text-slate-500">{children}</div>;
    },
  },
);

export const KPI = Object.assign(
  function KPIRoot({ children, className = "" }: { children: ReactNode; className?: string }) {
    return <div className={`rounded-lg p-4 ${className}`}>{children}</div>;
  },
  {
    Header({ children }: { children: ReactNode }) {
      return <div className="mb-3 flex items-center gap-2">{children}</div>;
    },
    Icon({ children, status }: { children: ReactNode; status: "success" | "warning" | "danger" }) {
      const color = status === "success" ? "text-success-soft-foreground bg-success-soft" : status === "warning" ? "text-warning-soft-foreground bg-warning-soft" : "text-danger-soft-foreground bg-danger-soft";
      return <div className={`grid h-8 w-8 place-items-center rounded-md ${color}`}>{children}</div>;
    },
    Title({ children }: { children: ReactNode }) {
      return <div className="text-xs font-medium uppercase text-slate-500">{children}</div>;
    },
    Content({ children }: { children: ReactNode }) {
      return <div>{children}</div>;
    },
    Value({ children }: { children: ReactNode; value: number }) {
      return <div className="text-2xl font-semibold text-slate-950">{children}</div>;
    },
  },
);

export const NativeSelect = Object.assign(
  function NativeSelectRoot({ children }: { children: ReactNode; fullWidth?: boolean; variant?: string }) {
    return <div>{children}</div>;
  },
  {
    Trigger({ children, onChange, value }: { children: ReactNode; onChange: (event: ChangeEvent<HTMLSelectElement>) => void; value: string }) {
      return (
        <select
          className="h-10 w-full rounded-md border border-slate-200 bg-surface px-3 text-sm text-slate-900 shadow-sm outline-none focus:border-slate-400"
          value={value}
          onChange={onChange}
        >
          {children}
        </select>
      );
    },
    Option({ children, value }: { children: ReactNode; value: string }) {
      return <option value={value}>{children}</option>;
    },
  },
);

export function MetricCard({
  icon,
  label,
  status,
  value,
}: {
  icon: ReactNode;
  label: string;
  status: "success" | "warning" | "danger";
  value: string;
}) {
  return (
    <KPI className="cube-card">
      <KPI.Header>
        <KPI.Icon status={status}>{icon}</KPI.Icon>
        <KPI.Title>{label}</KPI.Title>
      </KPI.Header>
      <KPI.Content>
        <KPI.Value value={Number(value)}>{value}</KPI.Value>
      </KPI.Content>
    </KPI>
  );
}

export function FieldLabel({ children, text }: { children: ReactNode; text: string }) {
  return (
    <label className="flex min-w-0 flex-col gap-1.5">
      <span className="text-xs font-medium text-slate-600">{text}</span>
      {children}
    </label>
  );
}

export function SignalLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="signal-line flex items-center justify-between gap-3 rounded-lg bg-slate-50 p-3">
      <span className="text-slate-500">{label}</span>
      <span className="min-w-0 truncate text-right font-medium text-slate-900">{value}</span>
    </div>
  );
}

export function CopyLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-lg bg-slate-50 p-3">
      <div className="min-w-0">
        <div className="mb-1 text-[11px] font-semibold uppercase text-slate-400">{label}</div>
        <div className="path-text font-mono text-xs text-slate-700">{value}</div>
      </div>
      <Button aria-label={`Copy ${label}`} size="sm" variant="secondary" onPress={() => copyText(value)}>
        <Copy size={14} />
      </Button>
    </div>
  );
}

export function QuotaRing({ label, value }: { label?: string; value?: number }) {
  const remaining = clampUIPercent(value);
  const degrees = remaining * 3.6;
  return (
    <div
      className="lb-quota-ring"
      style={{ background: `conic-gradient(#10b981 ${degrees}deg, #e2e8f0 0deg)` }}
    >
      <span>{label || (value === undefined ? "-" : `${Math.round(remaining)}%`)}</span>
    </div>
  );
}
