import { motion } from "framer-motion";
import { RotateCcw, Trash2, Loader2, X } from "lucide-react";
import type { Task } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { usePerformedProgress, useElapsed } from "./use-performed-progress";

function stageLabel(p: number, kind: string): string {
  if (kind === "video") {
    if (p < 12) return "排队 · 准备中";
    if (p < 30) return "读取源图";
    if (p < 55) return "推理动作轨迹";
    if (p < 80) return "渲染视频帧";
    if (p < 100) return "收尾处理";
    return "完成";
  }
  if (p < 12) return "排队 · 准备中";
  if (p < 28) return "分析参考图";
  if (p < 48) return "构思画面";
  if (p < 70) return "绘制中";
  if (p < 88) return "细节润色";
  if (p < 100) return "颜色适配 · 收尾";
  return "完成";
}

// TaskCard renders a running placeholder (skeleton + performed progress) or a
// failed card (error + retry/remove) in the grid view. The timeline view has its
// own active-node renderer; this is the grid equivalent.
export function TaskCard({ task }: { task: Task }) {
  const app = useApp();
  const pct = usePerformedProgress(task);
  const elapsed = useElapsed(task);
  const failed = task.status === "failed";

  return (
    <motion.div
      layout
      initial={{ opacity: 0, scale: 0.97 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.97 }}
      transition={{ duration: 0.2, ease: "easeOut" }}
      className="flex aspect-square flex-col overflow-hidden rounded-lg border border-line bg-bg-elev"
    >
      <div className="relative flex-1 overflow-hidden">
        {!failed && (
          <>
            <div className="absolute inset-0 bg-bg-elev-2" />
            <div className="absolute inset-0 -translate-x-full animate-shimmer bg-gradient-to-r from-transparent via-fg/10 to-transparent" />
            <div className="absolute inset-0 grid place-items-center">
              <Loader2 className="size-6 animate-spin text-accent/80" />
            </div>
          </>
        )}
      </div>
      <div className="space-y-1.5 p-2.5 text-xs">
        {!failed ? (
          <>
            <div className="flex items-center gap-2">
              <span className="text-fg-dim">{task.note || stageLabel(pct, task.kind)}</span>
              <button
                type="button"
                title="取消任务"
                onClick={() => app.removeTask(task.id)}
                className="ml-auto grid size-5 shrink-0 place-items-center rounded text-fg-mute transition-colors hover:bg-bg hover:text-danger"
              >
                <X className="size-3.5" />
              </button>
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-bg">
              <div className="h-full rounded-full bg-accent transition-[width] duration-300 ease-linear" style={{ width: `${pct}%` }} />
            </div>
            <div className="flex items-center justify-between tabular-nums text-accent">
              <span>{pct}%</span>
              {elapsed && <span className="text-fg-mute">{elapsed}</span>}
            </div>
          </>
        ) : (
          <>
            <div className="font-medium text-danger">失败</div>
            {task.error && <div className="line-clamp-2 leading-relaxed text-fg-mute">{task.error}</div>}
            <div className="flex gap-1.5 pt-0.5">
              <Button variant="danger" size="xs" onClick={() => app.retryTask(task.id)}>
                <RotateCcw className="size-3" /> 重试
              </Button>
              <Button variant="outline" size="xs" onClick={() => app.removeTask(task.id)}>
                <Trash2 className="size-3" /> 移除
              </Button>
            </div>
          </>
        )}
      </div>
    </motion.div>
  );
}
