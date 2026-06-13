import { motion } from "framer-motion";
import { RotateCcw, Trash2 } from "lucide-react";
import type { Task } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { usePerformedProgress } from "./use-performed-progress";

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
// failed card (error + retry/remove).
export function TaskCard({ task }: { task: Task }) {
  const app = useApp();
  const pct = usePerformedProgress(task);
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
          <div className="absolute inset-0 animate-pulse bg-gradient-to-br from-bg-elev-2 to-bg-elev" />
        )}
      </div>
      <div className="space-y-1.5 p-2.5 text-xs">
        {!failed ? (
          <>
            <div className="text-fg-dim">{stageLabel(pct, task.kind)}</div>
            <div className="h-1 overflow-hidden rounded-full bg-bg">
              <div className="h-full rounded-full bg-accent transition-[width] duration-300 ease-linear" style={{ width: `${pct}%` }} />
            </div>
            <div className="tabular-nums text-accent">{pct}%</div>
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
