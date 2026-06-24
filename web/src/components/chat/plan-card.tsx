import { Check, X, Loader2, Minus } from "lucide-react";
import type { PlanItem, PlanStepStatus } from "@/lib/types";

// StepIcon mirrors the adapt-pipeline visual vocabulary so the two cards read
// consistently: spinner=active, check=done, amber-x=failed, dash=skipped, hollow
// ring=pending.
function StepIcon({ status }: { status: PlanStepStatus }) {
  if (status === "done") return <Check className="size-3.5 text-ok" />;
  if (status === "failed") return <X className="size-3.5 text-amber-500" />;
  if (status === "running") return <Loader2 className="size-3.5 animate-spin text-accent" />;
  if (status === "skipped") return <Minus className="size-3.5 text-fg-mute/40" />;
  return <span className="size-3.5 rounded-full border border-fg-mute/30" />;
}

// PlanCard renders a submit_plan multi-step orchestration as an ordered
// checklist that lights up step by step from plan_* events. The header reflects
// the overall plan state (执行中 / 已完成 / 已中断).
export function PlanCard({ item }: { item: PlanItem }) {
  const header =
    item.status === "completed" ? "已完成" : item.status === "aborted" ? "已中断" : "执行中";
  const headerTone =
    item.status === "completed"
      ? "text-ok"
      : item.status === "aborted"
        ? "text-amber-500"
        : "text-accent";

  return (
    <div className="rounded-lg border border-line/50 bg-bg-elev/40 px-4 py-3 space-y-2.5 transition-all duration-200 ease-out">
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-widest text-fg-mute/60">执行计划</span>
        <span className={"text-[11px] font-medium " + headerTone}>{header}</span>
      </div>
      <ol className="space-y-1.5">
        {item.steps.map((s, i) => (
          <li key={s.id} className="flex items-start gap-2.5 text-[13px]">
            <span className="mt-0.5 shrink-0">
              <StepIcon status={s.status} />
            </span>
            <span
              className={
                "leading-relaxed " +
                (s.status === "running"
                  ? "text-fg"
                  : s.status === "done"
                    ? "text-fg-dim"
                    : s.status === "failed"
                      ? "text-amber-600 dark:text-amber-400"
                      : s.status === "skipped"
                        ? "text-fg-mute/40 line-through"
                        : "text-fg-mute/60")
              }
            >
              <span className="text-fg-mute/50 mr-1">{i + 1}.</span>
              {s.title}
              {s.status === "failed" && s.reason && (
                <span className="block text-[11px] text-amber-500/90 mt-0.5">失败：{s.reason}</span>
              )}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}
