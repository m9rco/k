import * as React from "react";
import { AnimatePresence, motion } from "framer-motion";
import { Plus, Sparkles, Send, X, ArrowUp, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Capybara } from "@/components/capybara/capybara";
import { useApp } from "@/store/context";
import { MAX_SELECTED } from "@/store/controller";
import * as api from "@/lib/api";
import { cn } from "@/lib/utils";

// Rotating example prompts shown as a typewriter in the empty onboarding input.
const DEMO_PROMPTS = [
  "把背景换成黄昏的赛博朋克城市…",
  "让图里的角色动起来，奔跑姿态…",
  "切成抖音和小红书的投放尺寸…",
  "根据图2和图3的风格生成一张新图…",
];

const prefersReducedMotion = () =>
  typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

// useTypewriterDemo cycles DEMO_PROMPTS as a typing/erasing placeholder while
// active. Returns the current demo string ("" when inactive). Reduced-motion
// shows the first prompt statically.
function useTypewriterDemo(active: boolean): string {
  const [demo, setDemo] = React.useState("");
  React.useEffect(() => {
    if (!active) { setDemo(""); return; }
    if (prefersReducedMotion()) { setDemo(DEMO_PROMPTS[0]); return; }
    let promptIdx = 0;
    let charIdx = 0;
    let erasing = false;
    let timer = 0;
    const tick = () => {
      const full = DEMO_PROMPTS[promptIdx];
      if (!erasing) {
        charIdx++;
        setDemo(full.slice(0, charIdx));
        if (charIdx >= full.length) {
          erasing = true;
          timer = window.setTimeout(tick, 1800);
          return;
        }
        timer = window.setTimeout(tick, 70);
      } else {
        charIdx--;
        setDemo(full.slice(0, charIdx));
        if (charIdx <= 0) {
          erasing = false;
          promptIdx = (promptIdx + 1) % DEMO_PROMPTS.length;
          timer = window.setTimeout(tick, 400);
          return;
        }
        timer = window.setTimeout(tick, 35);
      }
    };
    timer = window.setTimeout(tick, 500);
    return () => window.clearTimeout(timer);
  }, [active]);
  return demo;
}

// Composer is the message input row plus the multi-reference status bar.
export function Composer({ onboarding = false }: { onboarding?: boolean }) {
  const app = useApp();
  const { state } = app;
  const [text, setText] = React.useState("");
  const [focused, setFocused] = React.useState(false);
  const [optimizing, setOptimizing] = React.useState(false);
  const fileRef = React.useRef<HTMLInputElement>(null);

  const selectedCount = state.selected.size;
  // Demo is active only in onboarding, with an empty, unfocused input.
  const demoActive = onboarding && !text && !focused;
  const demoText = useTypewriterDemo(demoActive);

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
    const refs = [...state.selected].slice(0, MAX_SELECTED);
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
    <div className="relative border-t border-line px-4 pb-4 pt-3">
      {/* Mascot perched above the input during onboarding; exits as the layout
          expands into the workspace. */}
      <AnimatePresence>
        {demoActive && (
          <motion.div
            key="capy"
            initial={{ opacity: 0, y: 12, scale: 0.8 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 12, scale: 0.8 }}
            transition={{ duration: 0.25, ease: "easeOut" }}
            className="pointer-events-none absolute -top-[58px] left-1/2 z-10 -translate-x-1/2"
          >
            <Capybara />
          </motion.div>
        )}
      </AnimatePresence>
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
      {state.queue.length > 0 && (
        <div className="mb-2 space-y-1">
          <div className="text-[11px] text-fg-mute">待发送队列（当前回合结束后自动发送）</div>
          {state.queue.map((q, i) => (
            <div key={q.id} className="flex items-center gap-1.5 rounded-md border border-line bg-bg-elev/60 px-2 py-1 text-xs">
              <span className="grid size-4 shrink-0 place-items-center rounded-full bg-bg text-[10px] text-fg-mute">{i + 1}</span>
              <span className="flex-1 truncate text-fg-dim" title={q.text}>{q.text}</span>
              {i > 0 && (
                <button type="button" title="提前到队首" onClick={() => app.promoteQueued(q.id)} className="text-fg-mute transition-colors hover:text-fg">
                  <ArrowUp className="size-3.5" />
                </button>
              )}
              <button type="button" title="打断当前并立即发送" onClick={() => app.interruptSend({ id: q.id })} className="text-fg-mute transition-colors hover:text-accent">
                <Zap className="size-3.5" />
              </button>
              <button type="button" title="移除" onClick={() => app.removeQueued(q.id)} className="text-fg-mute transition-colors hover:text-danger">
                <X className="size-3.5" />
              </button>
            </div>
          ))}
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
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          placeholder={demoActive && demoText ? demoText : "例如：把背景换成夜晚的赛博朋克城市"}
          className={cn(
            "h-9 flex-1 rounded-md border border-line bg-bg-elev px-3 text-[13px] text-fg outline-none placeholder:text-fg-mute focus:border-accent/60",
            demoActive && "placeholder:text-fg-dim",
          )}
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
        {state.thinking && text.trim() && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="icon"
                title="打断当前回合并立即发送"
                onClick={() => {
                  const refs = [...state.selected].slice(0, MAX_SELECTED);
                  app.interruptSend({ text, ref: refs.length ? refs : undefined });
                  setText("");
                }}
              >
                <Zap />
              </Button>
            </TooltipTrigger>
            <TooltipContent>打断当前并发送</TooltipContent>
          </Tooltip>
        )}
        <Button type="submit" size="icon" title={state.thinking ? "加入队列（回合结束后发送）" : "发送"}>
          <Send />
        </Button>
      </form>
    </div>
  );
}
