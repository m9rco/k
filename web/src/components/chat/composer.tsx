import * as React from "react";
import { Plus, Sparkles, Send, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useApp } from "@/store/context";
import * as api from "@/lib/api";
import { cn } from "@/lib/utils";

// Composer is the message input row plus the multi-reference status bar.
export function Composer() {
  const app = useApp();
  const { state } = app;
  const [text, setText] = React.useState("");
  const [optimizing, setOptimizing] = React.useState(false);
  const fileRef = React.useRef<HTMLInputElement>(null);

  const selectedCount = state.selected.size;

  // Next-step suggestion chips based on the newest asset (fills the input).
  const suggestions = React.useMemo<string[]>(() => {
    const assets = [...state.assets.values()];
    if (assets.length === 0) return [];
    if (selectedCount > 1) return [];
    const newest = assets[assets.length - 1];
    switch (newest?.kind) {
      case "upload":
        return ["把这张图的背景换成", "把这张图的角色换成"];
      case "generated":
        return ["让这张图里的角色动起来", "再生成一张类似风格的"];
      case "cropped":
        return ["再切其他平台尺寸"];
      default:
        return [];
    }
  }, [state.assets, selectedCount]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const refs = [...state.selected].slice(0, 6);
    app.sendMessage(text, refs.length ? refs : undefined);
    setText("");
  };

  const optimize = async () => {
    const original = text.trim();
    if (!original) {
      app.toast("先输入一句想做什么，我来帮你润色成提示词", "warn");
      return;
    }
    if (optimizing) return;
    setOptimizing(true);
    try {
      const optimized = await api.optimizePrompt(state.sessionId, original);
      if (optimized && optimized !== original) {
        setText(optimized);
        app.toast("已优化提示词，确认后发送", "ok");
      } else {
        app.toast("提示词已经挺到位了，无需优化", "ok");
      }
    } catch (e) {
      app.toast("优化失败：" + (e as Error).message);
    } finally {
      setOptimizing(false);
    }
  };

  return (
    <div className="border-t border-line px-4 pb-4 pt-3">
      {suggestions.length > 0 && !text && (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {suggestions.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setText(s)}
              className="rounded-full border border-dashed border-line px-2.5 py-1 text-xs text-fg-dim transition-colors hover:border-solid hover:border-accent/50 hover:text-fg"
            >
              {s}
            </button>
          ))}
        </div>
      )}
      {selectedCount > 0 && (
        <div className="mb-2 flex items-center gap-2 text-xs">
          <span className="font-medium text-accent">
            {selectedCount >= 2 ? `已选 ${selectedCount} 张作为参考改图` : "已选 1 张作为参考"}
          </span>
          <button
            type="button"
            onClick={app.clearSelection}
            className="ml-auto flex items-center gap-1 rounded-full border border-line px-2 py-0.5 text-fg-mute transition-colors hover:border-danger hover:text-danger"
          >
            <X className="size-3" /> 清除
          </button>
        </div>
      )}
      <form onSubmit={submit} className="flex items-center gap-2">
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files?.length) app.uploadFiles(e.target.files);
            e.target.value = "";
          }}
        />
        <Button type="button" variant="ghost" size="icon" title="上传图片" onClick={() => fileRef.current?.click()}>
          <Plus />
        </Button>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="例如：把背景换成夜晚的赛博朋克城市"
          className="h-9 flex-1 rounded-md border border-line bg-bg-elev px-3 text-[13px] text-fg outline-none placeholder:text-fg-mute focus:border-accent/60"
        />
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={optimize}
              className={cn(optimizing && "opacity-50")}
            >
              <Sparkles />
            </Button>
          </TooltipTrigger>
          <TooltipContent>优化提示词</TooltipContent>
        </Tooltip>
        <Button
          type="button"
          variant={state.lossless ? "subtle" : "ghost"}
          size="sm"
          onClick={() => app.setLossless(!state.lossless)}
          title="无损压缩：开启时对 PNG 产物做无损优化"
        >
          {state.lossless ? "无损" : "原图"}
        </Button>
        <Button type="submit" size="icon" title="发送">
          <Send />
        </Button>
      </form>
    </div>
  );
}
