import * as React from "react";
import { motion } from "framer-motion";
import { ChevronRight, Sparkles, Pencil, CornerDownLeft, X, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// ReportBlock is a collapsible marketing-analysis panel for the stamp-mode
// reference area. It shares none of the chat AnalysisBlock's adaptation
// machinery (confirm countdown, re-analyze, gate) — here the report is
// informational and updates as the user changes the selected reference group.
// It DOES offer a lightweight read-write affordance: an inline "编辑" editor that
// saves the edited text back via onSave (persisted under the group's cache key,
// so the adapt flow reuses the edit). The caller hides the block entirely on
// error / unconfigured vision.
export function ReportBlock({
  title,
  text,
  loading,
  onSave,
  onReanalyze,
}: {
  title: string;
  text: string;
  loading?: boolean;
  onSave?: (report: string) => Promise<void> | void;
  onReanalyze?: () => Promise<void> | void;
}) {
  const [collapsed, setCollapsed] = React.useState(false);
  const [editing, setEditing] = React.useState(false);
  const [draft, setDraft] = React.useState(text);
  const [saving, setSaving] = React.useState(false);
  const [reanalyzing, setReanalyzing] = React.useState(false);
  const taRef = React.useRef<HTMLTextAreaElement>(null);

  // The editor opens (and the block expands) when entering edit mode; sync the
  // draft to the latest report each time so an edit always starts from current.
  // Also re-sync while editing when an external reanalyze swaps in fresh text.
  React.useEffect(() => {
    if (editing) {
      setDraft(text);
      requestAnimationFrame(() => taRef.current?.focus());
    }
  }, [editing, text]);

  const open = !collapsed || editing;
  const canEdit = !!onSave && !loading && !!text;

  const submitEdit = async () => {
    const v = draft.trim();
    if (!v || !onSave) {
      setEditing(false);
      return;
    }
    setSaving(true);
    try {
      await onSave(v);
      setEditing(false);
    } finally {
      setSaving(false);
    }
  };

  const doReanalyze = async () => {
    if (!onReanalyze) return;
    setReanalyzing(true);
    try {
      await onReanalyze();
    } finally {
      setReanalyzing(false);
    }
  };

  return (
    <motion.div
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
      className="mt-2.5 rounded-lg border border-line/60 bg-bg-elev/40"
    >
      <button
        type="button"
        onClick={() => setCollapsed((c) => !c)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-fg-mute transition-colors duration-200 ease-out hover:text-fg-dim"
      >
        <Sparkles className="size-3.5" />
        <span>{title}</span>
        {loading && (
          <span className="size-3.5 animate-spin rounded-full border-2 border-accent/30 border-t-accent" />
        )}
        {canEdit && !editing && (
          <span
            role="button"
            tabIndex={0}
            onClick={(e) => { e.stopPropagation(); setEditing(true); }}
            onKeyDown={(e) => { if (e.key === "Enter") { e.stopPropagation(); setEditing(true); } }}
            className="ml-2 inline-flex items-center gap-1 rounded-md border border-line bg-bg px-2 py-0.5 text-fg-dim transition-all duration-200 ease-out hover:border-accent hover:text-fg"
          >
            <Pencil className="size-3" /> 编辑
          </span>
        )}
        <ChevronRight className={cn("ml-auto size-3.5 transition-transform", open && "rotate-90")} />
      </button>

      {open && !editing && (
        <div className="whitespace-pre-wrap px-3 pb-3 text-[13px] leading-relaxed text-fg-mute">
          {text || (loading ? "分析中…" : "")}
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
              if (e.key === "Escape") setEditing(false);
            }}
            rows={Math.min(10, Math.max(4, draft.split("\n").length))}
            disabled={saving}
            className="w-full resize-y rounded-md border border-line bg-bg px-2.5 py-2 text-[13px] leading-relaxed text-fg outline-none transition-all duration-200 ease-out focus:border-accent disabled:opacity-50"
          />
          <div className="flex items-center justify-end gap-2">
            <span className="mr-auto text-xs text-fg-mute">按 4 行格式修改：核心主题 / 主体 / 宣发意图 / 必须保留</span>
            {onReanalyze && (
              <Button
                size="sm"
                variant="ghost"
                onClick={doReanalyze}
                disabled={saving || reanalyzing}
                title="重新调用视觉模型分析当前参考图组合"
                className="gap-1"
              >
                <RefreshCw className={cn("size-3.5", reanalyzing && "animate-spin")} />
                {reanalyzing ? "分析中…" : "重新分析"}
              </Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)} disabled={saving || reanalyzing} className="gap-1">
              <X className="size-3.5" /> 取消
            </Button>
            <Button size="sm" variant="default" onClick={submitEdit} disabled={saving || reanalyzing} title="保存修改 (⌘/Ctrl+Enter)" className="gap-1">
              <CornerDownLeft className="size-3.5" /> {saving ? "保存中…" : "保存"}
            </Button>
          </div>
        </div>
      )}
    </motion.div>
  );
}
