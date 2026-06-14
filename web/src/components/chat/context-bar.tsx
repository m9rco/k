import * as React from "react";
import { Eraser, SlidersHorizontal } from "lucide-react";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ModelPicker } from "./model-picker";

// ContextBar shows connection status and context-window usage, with a clear
// action that resets the conversation window (workspace untouched) and a model
// settings entry that opens the per-scene model picker.
export function ContextBar() {
  const { state, clearContext } = useApp();
  const ctx = state.context;
  const pct = ctx && ctx.budget > 0 ? Math.min(100, Math.round((ctx.estimatedTokens / ctx.budget) * 100)) : 0;
  const [pickerOpen, setPickerOpen] = React.useState(false);

  return (
    <div className="flex items-center gap-2.5 border-b border-line px-4 py-2.5 text-xs text-fg-dim">
      <span
        className={cn(
          "size-2 rounded-full",
          state.connected ? "bg-ok" : "bg-fg-mute",
        )}
        title={state.connected ? "已连接" : "连接中…"}
      />
      <span>{state.connected ? "已连接" : "连接中…"}</span>
      {ctx && (
        <span className="text-fg-mute">
          上下文 {pct}%{ctx.compressed ? " · 已压缩" : ""}
        </span>
      )}
      <Button
        variant="ghost"
        size="xs"
        className="ml-auto"
        onClick={() => setPickerOpen(true)}
        title="模型设置：为每个场景选择模型"
      >
        <SlidersHorizontal className="size-3.5" /> 模型
      </Button>
      <Button
        variant="ghost"
        size="xs"
        onClick={() => {
          if (confirm("清理当前会话上下文？将开始新话题，工作区素材不受影响。")) clearContext();
        }}
      >
        <Eraser className="size-3.5" /> 清上下文
      </Button>
      <ModelPicker open={pickerOpen} onOpenChange={setPickerOpen} />
    </div>
  );
}
