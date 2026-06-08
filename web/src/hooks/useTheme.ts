import { useEffect, useState } from "react";

// Theme persistence. The choice is stored in localStorage and reflected on
// <html> via the .light/.dark class + data-theme attribute that the CSS tokens
// key off. Defaults to dark, falling back to the OS preference.

export type ThemeMode = "light" | "dark";
const THEME_KEY = "cube20.theme";

function preferredTheme(): ThemeMode {
  if (typeof window === "undefined") return "dark";
  const stored = window.localStorage.getItem(THEME_KEY);
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function applyTheme(mode: ThemeMode) {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.classList.toggle("dark", mode === "dark");
  root.classList.toggle("light", mode === "light");
  root.setAttribute("data-theme", mode);
}

export function useTheme(): [ThemeMode, () => void] {
  const [mode, setMode] = useState<ThemeMode>(() => preferredTheme());
  useEffect(() => {
    applyTheme(mode);
    if (typeof window !== "undefined") window.localStorage.setItem(THEME_KEY, mode);
  }, [mode]);
  const toggle = () => setMode((current) => (current === "dark" ? "light" : "dark"));
  return [mode, toggle];
}
