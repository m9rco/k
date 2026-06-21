import { Check, X, Loader2, Layers } from "lucide-react";
import { motion } from "framer-motion";
import { useApp } from "@/store/context";
import type { VariantsGroupItem } from "@/lib/types";

// VariantChip shows one variant's live status (queued/running → spinner,
// done → check, failed → cross) plus its dimension label. The whole batch is one
// group so the buyer can scan all N creative versions at a glance; products fill
// into the workspace independently as each task completes.
function VariantChip({ taskId, label }: { taskId: string; label: string }) {
  const { state } = useApp();
  const task = state.tasks.get(taskId);
  const status = task?.status;

  const icon =
    status === "done" ? (
      <Check className="size-3 text-ok" />
    ) : status === "failed" ? (
      <X className="size-3 text-warn" />
    ) : (
      <Loader2 className="size-3 animate-spin text-accent" />
    );

  const tone =
    status === "done"
      ? "text-fg-dim border-line/60"
      : status === "failed"
        ? "text-warn border-warn/30"
        : "text-accent border-accent/30";

  return (
    <div
      title={status === "failed" ? task?.error || "生成失败" : undefined}
      className={
        "flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] transition-all duration-200 ease-out " +
        tone
      }
    >
      {icon}
      <span className="truncate">{label}</span>
    </div>
  );
}

// VariantsGroup renders a generate_variants batch as one grouped, comparable
// cluster. labels[i] pairs with taskIds[i]; when labels are missing (older
// payloads) it falls back to a 1-based index.
export function VariantsGroup({ item }: { item: VariantsGroupItem }) {
  const total = item.taskIds.length;
  return (
    <motion.div
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.2, ease: "easeOut" }}
      className="rounded-md border border-line/50 bg-bg-elev/40 px-3 py-2 space-y-2"
    >
      <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-widest text-fg-mute/60">
        <Layers className="size-3" />
        <span>批量变体 · {item.dimension} × {total}</span>
      </div>
      <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-4">
        {item.taskIds.map((id, i) => (
          <VariantChip key={id} taskId={id} label={item.labels[i] || `变体 ${i + 1}`} />
        ))}
      </div>
    </motion.div>
  );
}
