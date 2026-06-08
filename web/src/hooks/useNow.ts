import { useEffect, useState } from "react";

// useNow returns a periodically-updating timestamp (ms) so countdown labels
// tick without a network round-trip. Pauses while the tab is hidden to avoid
// pointless re-renders. Default cadence is 30s — fine-grained enough for
// minute-level reset countdowns.
export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = () => {
      if (typeof document !== "undefined" && document.hidden) return;
      setNow(Date.now());
    };
    const timer = window.setInterval(tick, intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);
  return now;
}
