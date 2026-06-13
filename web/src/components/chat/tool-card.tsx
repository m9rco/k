import { motion } from "framer-motion";
import { Loader2, Check, X } from "lucide-react";
import type { ToolCardData } from "@/lib/types";
import { toolMeta, toolSubtitle } from "@/lib/tool-meta";
import { cn } from "@/lib/utils";

// ToolCard shows a tool invocation as an icon + Chinese phrase + readable
// subtitle, with a lifecycle indicator (running → done/failed). No raw JSON.
export function ToolCard({ tool }: { tool: ToolCardData }) {
  const meta = toolMeta(tool.name, tool.args);
  const Icon = meta.icon;
  const sub = toolSubtitle(tool.name, tool.args);
  const detail = tool.status === "failed" ? tool.error : tool.summary;

  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
      className={cn(
        "rounded-lg border bg-bg-elev/60 px-3 py-2.5",
        tool.status === "failed" ? "border-danger/40" : "border-line/70",
      )}
    >
      <div className="flex items-center gap-2.5">
        <Icon className="size-4 text-fg-dim" />
        <span className="text-[13px] font-medium text-fg">{meta.title}</span>
        <span className="ml-auto flex items-center">
          {tool.status === "running" && <Loader2 className="size-3.5 animate-spin text-accent" />}
          {tool.status === "done" && <Check className="size-3.5 text-ok" />}
          {tool.status === "failed" && <X className="size-3.5 text-danger" />}
        </span>
      </div>
      {sub && <div className="mt-1 pl-[26px] text-xs leading-relaxed text-fg-mute">{sub}</div>}
      {detail && tool.status === "failed" && (
        <div className="mt-1 pl-[26px] text-xs leading-relaxed text-danger/80">{detail}</div>
      )}
    </motion.div>
  );
}
