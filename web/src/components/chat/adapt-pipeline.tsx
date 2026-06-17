import * as React from "react";
import { Check, X, Loader2, Minus } from "lucide-react";
import { useApp } from "@/store/context";
import type { AdaptPipelineItem } from "@/lib/types";

// Each adapt task goes through up to 4 stages; we derive the active stage from
// the task's stage + review fields so no extra state is needed. 补全 (outpaint)
// is conditional — only extreme-ratio reshapes need it; near-ratio sizes
// converge by plain scale and the 补全 step is shown as 跳过.
type StageStatus = "pending" | "active" | "done" | "error" | "skipped";

function stagesFor(
  task: { status: string; stage?: string; review?: string; outpainted?: boolean } | undefined,
): StageStatus[] {
  if (!task) return ["pending", "pending", "pending", "pending"];
  const done = task.status === "done";
  const failed = task.status === "failed";

  // 分析: always done once the task exists
  const s0: StageStatus = "done";

  // 生图(gpt-image-2)
  const genDone = task.stage === "outpainting" || task.stage === "reviewing" || done || failed;
  const s1: StageStatus = failed && !genDone ? "error" : genDone ? "done" : "active";

  // 补全 (outpaint — conditional). When the task converged without ever emitting
  // outpaint_started, this size didn't need it → skipped, not done.
  const outActive = task.stage === "outpainting";
  let s2: StageStatus;
  if (outActive) s2 = "active";
  else if (task.outpainted) s2 = task.stage === "reviewing" || done ? "done" : "pending";
  else if (task.stage === "reviewing" || done) s2 = "skipped";
  else s2 = "pending";

  // 质量审核
  const reviewDone = done || (task.review === "passed" && !task.stage);
  const reviewActive = task.stage === "reviewing" || task.review === "checking";
  const reviewFailed = task.review === "failed";
  const s3: StageStatus = reviewDone ? "done" : reviewActive ? "active" : reviewFailed ? "error" : "pending";

  return [s0, s1, s2, s3];
}

const STEPS = ["分析", "生图", "补全", "质量审核"];

function StepIcon({ status }: { status: StageStatus }) {
  if (status === "done") return <Check className="size-3 text-ok" />;
  if (status === "error") return <X className="size-3 text-amber-500" />;
  if (status === "active") return <Loader2 className="size-3 animate-spin text-accent" />;
  if (status === "skipped") return <Minus className="size-3 text-fg-mute/40" />;
  return <span className="size-3 rounded-full border border-fg-mute/30" />;
}

function TaskPipeline({ taskId }: { taskId: string }) {
  const { state } = useApp();
  const task = state.tasks.get(taskId);
  const stages = stagesFor(task);

  // When review failed and the task is still running, a retry is in progress.
  const retrying = task?.review === "failed" && task?.status === "running";

  return (
    <div className="flex items-center gap-1.5 text-[11px]">
      {STEPS.map((label, i) => (
        <React.Fragment key={label}>
          <div
            title={i === 3 && stages[i] === "error" && task?.reviewReason ? task.reviewReason : undefined}
            className={
              "flex items-center gap-1 " +
              (stages[i] === "active"
                ? "text-accent"
                : stages[i] === "done"
                  ? "text-fg-dim"
                  : stages[i] === "skipped"
                    ? "text-fg-mute/40 line-through"
                    : "text-fg-mute/50")
            }
          >
            <StepIcon status={stages[i]} />
            <span>{label}</span>
          </div>
          {i < STEPS.length - 1 && <span className="text-fg-mute/30">→</span>}
        </React.Fragment>
      ))}
      {retrying && (
        <span className="text-[10px] text-amber-500/80 ml-1">按建议重绘中…</span>
      )}
    </div>
  );
}

export function AdaptPipeline({ item }: { item: AdaptPipelineItem }) {
  return (
    <div className="rounded-md border border-line/50 bg-bg-elev/40 px-3 py-2 space-y-1.5">
      <span className="text-[10px] uppercase tracking-widest text-fg-mute/60">适配流程</span>
      {item.taskIds.map((id) => (
        <TaskPipeline key={id} taskId={id} />
      ))}
    </div>
  );
}
