import * as React from "react";
import { motion } from "framer-motion";
import { ChevronRight, Sparkles, Pencil, CornerDownLeft, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// AnalysisBlock renders the marketing-analysis (grok) report as a collapsible
// block, mirroring ReasoningBlock's look. While streaming it stays expanded;
// once done it folds to a "宣发分析" summary row.
//
// When the backend opens the editable confirmation window (confirming=true),
// the block surfaces a 3s countdown plus an "编辑" entry: leaving it untouched
// lets the parent's countdown auto-submit the original (zero-click), while
// clicking 编辑 pauses the countdown and opens an inline editor pre-filled with
// the report so the user can rewrite it by the fixed 4-line format before
// submitting. Once confirmed the controls disappear (single confirm only).
export function AnalysisBlock({
  text,
  collapsed,
  done,
  confirming,
  secondsLeft,
  editing,
  confirmed,
  reanalyzing,
  onToggle,
  onEdit,
  onSubmit,
  onReanalyze,
}: {
  text: string;
  collapsed: boolean;
  done: boolean;
  confirming?: boolean;
  secondsLeft?: number;
  editing?: boolean;
  confirmed?: boolean;
  reanalyzing?: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onSubmit: (summary: string, edited: boolean) => void;
  onReanalyze?: () => void;
}) {
  const [draft, setDraft] = React.useState(text);
  const taRef = React.useRef<HTMLTextAreaElement>(null);

  // Sync the editor with the latest report text when entering edit mode.
  React.useEffect(() => {
    if (editing) {
      setDraft(text);
      // Focus on the next frame so the textarea is mounted.
      requestAnimationFrame(() => taRef.current?.focus());
    }
  }, [editing, text]);

  const label = done ? "宣发分析" : "分析中";
  // The block auto-expands while editing so the user sees the full editor even
  // after the done-fold collapsed it.
  const open = !collapsed || editing;

  const submitEdit = () => {
    const v = draft.trim();
    onSubmit(v || text, v !== "" && v !== text);
  };

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
        <Sparkles className="size-3.5" />
        <span>{label}</span>
        {/* Confirmation affordances live in the header so they stay visible even
            when the report body is collapsed. */}
        {confirming && !confirmed && !editing && (
          <span className="ml-2 flex items-center gap-2 text-fg-dim">
            <span className="tabular-nums">{secondsLeft ?? 0}s 后自动采用</span>
            <span
              role="button"
              tabIndex={0}
              onClick={(e) => { e.stopPropagation(); onEdit(); }}
              onKeyDown={(e) => { if (e.key === "Enter") { e.stopPropagation(); onEdit(); } }}
              className="inline-flex items-center gap-1 rounded-md border border-line bg-bg px-2 py-0.5 text-fg-dim transition-all duration-200 ease-out hover:border-accent hover:text-fg"
            >
              <Pencil className="size-3" /> 编辑
            </span>
          </span>
        )}
        <ChevronRight className={cn("ml-auto size-3.5 transition-transform", open && "rotate-90")} />
      </button>

      {open && !editing && (
        <div className="whitespace-pre-wrap px-3 pb-3 text-[13px] leading-relaxed text-fg-mute">
          {text}
        </div>
      )}

      {editing && (
        <div className="flex flex-col gap-2 px-3 pb-3">
          <textarea
            ref={taRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submitEdit();
            }}
            rows={Math.min(8, Math.max(4, draft.split("\n").length))}
            disabled={reanalyzing}
            className="w-full resize-y rounded-md border border-line bg-bg px-2.5 py-2 text-[13px] leading-relaxed text-fg outline-none transition-all duration-200 ease-out focus:border-accent disabled:opacity-50"
          />
          <div className="flex items-center justify-end gap-2">
            <span className="mr-auto text-xs text-fg-mute">按 4 行格式修改：核心主题 / 主体 / 宣发意图 / 必须保留</span>
            {onReanalyze && (
              <Button
                size="sm"
                variant="ghost"
                onClick={onReanalyze}
                disabled={reanalyzing}
                title="重新调用 grok 分析同一批参考图"
                className="gap-1"
              >
                <RefreshCw className={cn("size-3.5", reanalyzing && "animate-spin")} />
                {reanalyzing ? "分析中…" : "重新分析"}
              </Button>
            )}
            <Button size="sm" variant="default" onClick={submitEdit} disabled={reanalyzing} title="提交修改并继续适配 (⌘/Ctrl+Enter)">
              <CornerDownLeft className="size-3.5" /> 用这份继续
            </Button>
          </div>
        </div>
      )}
    </motion.div>
  );
}
