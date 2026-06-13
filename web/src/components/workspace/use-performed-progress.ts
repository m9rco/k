import * as React from "react";
import type { Task } from "@/lib/types";

// usePerformedProgress drives a time-based "performed" progress bar that climbs
// smoothly with easing + jitter toward ~92%, decoupled from the backend's
// coarse 30/45/80 events (which only raise the floor). On done it rolls to 100.
// This makes a ~1–2 min generation feel alive rather than stuck at 45%.
export function usePerformedProgress(task: Task): number {
  const [shown, setShown] = React.useState(task.progress || 2);
  const shownRef = React.useRef(shown);
  shownRef.current = shown;

  const running = task.status === "running" || task.status === "queued";

  React.useEffect(() => {
    if (task.status === "done") {
      // Quick roll to 100 from wherever we are.
      let cur = shownRef.current;
      const iv = window.setInterval(() => {
        cur = Math.min(100, cur + 6);
        setShown(cur);
        if (cur >= 100) window.clearInterval(iv);
      }, 24);
      return () => window.clearInterval(iv);
    }
    if (!running) return;
    const iv = window.setInterval(() => {
      setShown((prev) => {
        const floor = task.progress || 0;
        let next = Math.max(prev, floor);
        const ceil = 92;
        if (next < ceil) {
          const remaining = ceil - next;
          const step = Math.random() < 0.15 ? 0 : remaining * 0.012 + Math.random() * 0.5;
          next = Math.min(ceil, next + step);
        }
        return next;
      });
    }, 240);
    return () => window.clearInterval(iv);
  }, [task.status, task.progress, running]);

  return Math.round(shown);
}
