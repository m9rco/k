import * as React from "react";
import { AnimatePresence, motion } from "framer-motion";
import { useApp } from "@/store/context";
import { BrandMark } from "@/components/brand-mark";
import { MessageBubble } from "./message-bubble";
import { ReasoningBlock } from "./reasoning-block";
import { ToolCard } from "./tool-card";
import { CapsuleBubble } from "./capsule-bubble";
import { FollowUpBubble } from "./follow-up-bubble";
import { LoadingBubble } from "./loading-bubble";
import { Composer } from "./composer";
import { ContextBar } from "./context-bar";

const CAPABILITIES = [
  "换背景 · 把图片背景替换成你描述的场景，自动做颜色适配",
  "换角色 · 替换图片中的主体，保留整体构图",
  "换文案 · 替换图片上的宣传文字",
  "切尺寸 · 按平台广告位尺寸裁剪，纯裁剪不经过 AI",
  "生视频 · 基于一张图加动作描述生成短视频",
  "搜图 · 按关键词搜索参考图并下载到工作区",
  "下载 / 打包 · 单张下载或批量打包 zip",
];

// BrandHero is the ceremonial brand reveal shown only in the immersive onboarding
// state: brand name + subtitle fade up, evoking a quiet "workshop awaiting you".
function BrandHero() {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.5, ease: "easeOut", delay: 0.3 }}
      className="flex flex-col items-center gap-3 py-8 text-center"
    >
      <BrandMark className="size-9 text-accent" />
      <div>
        <h1 className="text-base font-semibold tracking-[0.2em] text-fg">GAME ASSET STUDIO</h1>
        <p className="mt-1 text-xs tracking-[0.3em] text-fg-mute/70">游 戏 宣 发 资 产 工 坊</p>
      </div>
    </motion.div>
  );
}

function Welcome() {
  return (
    <div className="mx-auto max-w-md py-4 text-center">
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

export function ChatPanel({ onboarding = false }: { onboarding?: boolean }) {
  const { state, collapseReasoningItem, collapseAnalysisItem, capsuleSelect, dismissFollowUp } = useApp();
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
      {!onboarding && <ContextBar />}
      <div ref={logRef} className={onboarding ? "flex flex-1 flex-col justify-center overflow-hidden px-4 py-4" : "flex-1 space-y-3 overflow-y-auto px-4 py-4"}>
        {onboarding && <BrandHero />}
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
            if (it.kind === "analysis")
              return (
                <ReasoningBlock
                  key={it.id}
                  text={it.text}
                  collapsed={it.collapsed}
                  done={it.done}
                  onToggle={() => collapseAnalysisItem(it.id)}
                  label={it.done ? "宣发分析" : "分析中"}
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
            if (it.kind === "follow_up")
              return (
                <FollowUpBubble
                  key={it.id}
                  message={it.message}
                  options={it.options}
                  dismissed={it.dismissed}
                  onSubmit={(value) => { dismissFollowUp(it.id); capsuleSelect(it.id, value); }}
                  onDismiss={() => dismissFollowUp(it.id)}
                />
              );
            if (it.kind === "loading") return <LoadingBubble key={it.id} level={it.level} />;
            return <ToolCard key={it.id} tool={it.tool} />;
          })}
        </AnimatePresence>
      </div>
      <Composer onboarding={onboarding} />
    </div>
  );
}
