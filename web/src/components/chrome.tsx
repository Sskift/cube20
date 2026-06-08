import type { ReactNode } from "react";
import { Moon, Sun } from "lucide-react";

import { useLang } from "../i18n";
import type { ThemeMode } from "../hooks/useTheme";

// App chrome: the theme + language toggles in the navbar and the sidebar nav
// button. Small, stateless, shared by both the admin and personal shells.

export function ThemeToggle({ mode, onToggle }: { mode: ThemeMode; onToggle: () => void }) {
  return (
    <button
      aria-label={mode === "dark" ? "Switch to light theme" : "Switch to dark theme"}
      className="grid h-8 w-8 shrink-0 place-items-center rounded-md border border-slate-200 bg-surface text-slate-600 shadow-sm transition-colors hover:bg-slate-50 hover:text-slate-950"
      type="button"
      onClick={onToggle}
    >
      {mode === "dark" ? <Sun size={15} /> : <Moon size={15} />}
    </button>
  );
}

// LangToggle flips the UI language. It shows the language it will switch TO (so
// in Chinese it reads "EN", in English it reads "中"), matching the ThemeToggle
// button's size and styling.
export function LangToggle() {
  const { lang, toggle } = useLang();
  return (
    <button
      aria-label={lang === "zh" ? "Switch to English" : "切换为中文"}
      className="grid h-8 w-8 shrink-0 place-items-center rounded-md border border-slate-200 bg-surface text-xs font-semibold text-slate-600 shadow-sm transition-colors hover:bg-slate-50 hover:text-slate-950"
      type="button"
      onClick={toggle}
    >
      {lang === "zh" ? "EN" : "中"}
    </button>
  );
}

export function NavItem({
  active,
  badge,
  icon,
  label,
  onPress,
}: {
  active?: boolean;
  badge?: string;
  icon: ReactNode;
  label: string;
  onPress: () => void;
}) {
  return (
    <button
      aria-current={active ? "page" : undefined}
      aria-pressed={active}
      className={`cube-nav-button flex h-10 w-full items-center gap-3 rounded-xl px-3 text-left text-sm ${
        active ? "cube-brand shadow-sm" : "text-slate-600 hover:bg-slate-100 hover:text-slate-950"
      }`}
      type="button"
      onClick={onPress}
    >
      <span className="grid h-6 w-6 place-items-center">{icon}</span>
      <span className="min-w-0 flex-1 truncate font-medium">{label}</span>
      {badge && (
        <span className={`rounded-full px-2 py-0.5 text-xs ${active ? "bg-white/15 text-white" : "bg-slate-200 text-slate-600"}`}>
          {badge}
        </span>
      )}
    </button>
  );
}
