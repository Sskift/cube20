import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";

// Lightweight in-app i18n with no third-party dependency. Strings are inlined at
// each call site as t(zh, en) — there is no central key table to keep in sync,
// so adding or changing a label is a single local edit and two translators can
// never collide on a key name. The chosen language is persisted to localStorage
// and defaults to the browser language (zh* -> Chinese, otherwise English).

export type Lang = "zh" | "en";

const STORAGE_KEY = "cube-lang";

function detectLang(): Lang {
  if (typeof window === "undefined") return "zh";
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === "zh" || stored === "en") return stored;
  } catch {
    // ignore storage access errors (private mode, etc.)
  }
  const nav = window.navigator?.language ?? "";
  return nav.toLowerCase().startsWith("zh") ? "zh" : "en";
}

interface LangContextValue {
  lang: Lang;
  setLang: (lang: Lang) => void;
  toggle: () => void;
  // t returns the Chinese or English variant for the active language.
  t: (zh: string, en: string) => string;
}

const LangContext = createContext<LangContextValue | null>(null);

export function LangProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(detectLang);

  const setLang = useCallback((next: Lang) => {
    setLangState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // ignore storage write errors
    }
  }, []);

  const toggle = useCallback(() => {
    setLangState((prev) => {
      const next = prev === "zh" ? "en" : "zh";
      try {
        window.localStorage.setItem(STORAGE_KEY, next);
      } catch {
        // ignore storage write errors
      }
      return next;
    });
  }, []);

  // Keep <html lang> in sync so the document advertises the active language.
  useEffect(() => {
    if (typeof document !== "undefined") {
      document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
    }
  }, [lang]);

  const t = useCallback((zh: string, en: string) => (lang === "zh" ? zh : en), [lang]);

  return (
    <LangContext.Provider value={{ lang, setLang, toggle, t }}>
      {children}
    </LangContext.Provider>
  );
}

export function useLang(): LangContextValue {
  const ctx = useContext(LangContext);
  if (!ctx) {
    throw new Error("useLang must be used within a LangProvider");
  }
  return ctx;
}

// Convenience hook for components that only need the translate function.
export function useT(): (zh: string, en: string) => string {
  return useLang().t;
}
