import { motion } from "framer-motion";
import { ChevronRight, Brain } from "lucide-react";
import { cn } from "@/lib/utils";

// ReasoningBlock renders the model's thinking as a dimmed, collapsible block,
// visually distinct from the final answer. Streams expanded, folds when done.
export function ReasoningBlock({
  text,
  collapsed,
  done,
  onToggle,
  label,
}: {
  text: string;
  collapsed: boolean;
  done: boolean;
  onToggle: () => void;
  label?: string;
}) {
  const displayLabel = label ?? (done ? "已思考" : "思考中");
  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
      className="rounded-lg border border-line/60 bg-bg-elev/40"
    >
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-fg-mute transition-colors hover:text-fg-dim"
      >
        <Brain className="size-3.5" />
        <span>{displayLabel}</span>
        <ChevronRight className={cn("ml-auto size-3.5 transition-transform", !collapsed && "rotate-90")} />
      </button>
      {!collapsed && (
        <div className="whitespace-pre-wrap px-3 pb-3 text-[13px] leading-relaxed text-fg-mute">
          {text}
        </div>
      )}
    </motion.div>
  );
}
