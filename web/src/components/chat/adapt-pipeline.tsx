import * as React from "react";
import { Check, X, Loader2 } from "lucide-react";
import { useApp } from "@/store/context";
import type { AdaptPipelineItem } from "@/lib/types";

// Each adapt task goes through up to 4 stages; we derive the active stage from
// the task's stage + review fields so no extra state is needed.
type StageStatus = "pending" | "active" | "done" | "error";

function stagesFor(task: { status: string; stage?: string; review?: string } | undefined): StageStatus[] {
  if (!task) return ["pending", "pending", "pending", "pending"];
  const done = task.status === "done";
  const failed = task.status === "failed";

  // 分析: always done once the task exists
  const s0: StageStatus = "done";

  // 生图(gpt-image-2)
  const genDone = task.stage === "outpainting" || task.stage === "reviewing" || done || failed;
  const s1: StageStatus = failed && !genDone ? "error" : genDone ? "done" : "active";

  // Gemini补全 (outpaint — only some tasks go through this)
  const outDone = task.stage === "reviewing" || (done && task.stage !== "outpainting");
  const outActive = task.stage === "outpainting";
  const s2: StageStatus = outDone ? "done" : outActive ? "active" : "pending";

  // 质量审核
  const reviewDone = done || (task.review === "passed" && !task.stage);
  const reviewActive = task.stage === "reviewing" || task.review === "checking";
  const reviewFailed = task.review === "failed";
  const s3: StageStatus = reviewDone ? "done" : reviewActive ? "active" : reviewFailed ? "error" : "pending";

  return [s0, s1, s2, s3];
}

const STEPS = ["分析", "生图", "Gemini补全", "质量审核"];

function StepIcon({ status }: { status: StageStatus }) {
  if (status === "done") return <Check className="size-3 text-ok" />;
  if (status === "error") return <X className="size-3 text-amber-500" />;
  if (status === "active") return <Loader2 className="size-3 animate-spin text-accent" />;
  return <span className="size-3 rounded-full border border-fg-mute/30" />;
}

function TaskPipeline({ taskId }: { taskId: string }) {
  const { state } = useApp();
  const task = state.tasks.get(taskId);
  const stages = stagesFor(task);

  return (
    <div className="flex items-center gap-1.5 text-[11px]">
      {STEPS.map((label, i) => (
        <React.Fragment key={label}>
          <div
            className={
              "flex items-center gap-1 " +
              (stages[i] === "active" ? "text-accent" : stages[i] === "done" ? "text-fg-dim" : "text-fg-mute/50")
            }
          >
            <StepIcon status={stages[i]} />
            <span>{label}</span>
          </div>
          {i < STEPS.length - 1 && (
            <span className="text-fg-mute/30">→</span>
          )}
        </React.Fragment>
      ))}
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
