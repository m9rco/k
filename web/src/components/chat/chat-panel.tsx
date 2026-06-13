import * as React from "react";
import { AnimatePresence } from "framer-motion";
import { useApp } from "@/store/context";
import { MessageBubble } from "./message-bubble";
import { ReasoningBlock } from "./reasoning-block";
import { ToolCard } from "./tool-card";
import { CapsuleBubble } from "./capsule-bubble";
import { LoadingBubble } from "./loading-bubble";
import { Composer } from "./composer";
import { ContextBar } from "./context-bar";

const CAPABILITIES = [
  "换背景 · 把图片背景替换成你描述的场景，自动做颜色适配",
  "换角色 · 替换图片中的主体，保留整体构图",
  "换文案 · 替换图片上的宣传文字",
  "切尺寸 · 按平台广告位尺寸裁剪，纯裁剪不经过 AI",
  "生视频 · 基于一张图加动作描述生成短视频",
  "下载 / 打包 · 单张下载或批量打包 zip",
];

function Welcome() {
  return (
    <div className="mx-auto max-w-md py-10 text-center">
      <p className="text-sm font-medium text-fg">嗨，我是你的宣发素材助手。</p>
      <p className="mt-1 text-[13px] leading-relaxed text-fg-dim">上传一张图，告诉我你想做什么：</p>
      <ul className="mt-4 space-y-1.5 text-left">
        {CAPABILITIES.map((c) => (
          <li key={c} className="rounded-md bg-bg-elev/50 px-3 py-2 text-xs leading-relaxed text-fg-dim">
            {c}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ChatPanel() {
  const { state, collapseReasoningItem, capsuleSelect } = useApp();
  const logRef = React.useRef<HTMLDivElement>(null);

  // Keep pinned to newest content when already near the bottom.
  React.useEffect(() => {
    const el = logRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 160;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [state.chat]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ContextBar />
      <div ref={logRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {state.chat.length === 0 && <Welcome />}
        <AnimatePresence initial={false}>
          {state.chat.map((it) => {
            if (it.kind === "user" || it.kind === "assistant")
              return <MessageBubble key={it.id} role={it.kind} text={it.text} streaming={"streaming" in it && it.streaming} />;
            if (it.kind === "reasoning")
              return (
                <ReasoningBlock
                  key={it.id}
                  text={it.text}
                  collapsed={it.collapsed}
                  done={it.done}
                  onToggle={() => collapseReasoningItem(it.id)}
                />
              );
            if (it.kind === "capsule")
              return (
                <CapsuleBubble
                  key={it.id}
                  question={it.question}
                  options={it.options}
                  answered={it.answered}
                  onSubmit={(value) => capsuleSelect(it.id, value)}
                />
              );
            if (it.kind === "loading") return <LoadingBubble key={it.id} />;
            return <ToolCard key={it.id} tool={it.tool} />;
          })}
        </AnimatePresence>
      </div>
      <Composer />
    </div>
  );
}
