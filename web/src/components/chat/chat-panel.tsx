import * as React from "react";
import { AnimatePresence } from "framer-motion";
import { useApp } from "@/store/context";
import { MessageBubble } from "./message-bubble";
import { ReasoningBlock } from "./reasoning-block";
import { AnalysisBlock } from "./analysis-block";
import { ToolCard } from "./tool-card";
import { CopyCard } from "./copy-card";
import { CapsuleBubble } from "./capsule-bubble";
import { FollowUpBubble } from "./follow-up-bubble";
import { LoadingBubble } from "./loading-bubble";
import { AdaptPipeline } from "./adapt-pipeline";
import { VariantsGroup } from "./variants-group";
import { PlanCard } from "./plan-card";
import { Composer } from "./composer";
import { ContextBar } from "./context-bar";
import { WorkspaceTips } from "./workspace-tips";
import { OnboardingHero } from "./onboarding-hero";

export function ChatPanel({ onboarding = false }: { onboarding?: boolean }) {
  const { state, collapseReasoningItem, collapseAnalysisItem, capsuleSelect, dismissFollowUp, editSummary, reanalyzeSummary, submitSummaryConfirm } = useApp();
  const logRef = React.useRef<HTMLDivElement>(null);

  // Keep pinned to newest content when already near the bottom.
  React.useEffect(() => {
    const el = logRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 160;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [state.chat]);

  // Immersive first screen: brand + centered input, until the first message
  // turns this into a normal chat stream.
  if (onboarding && state.chat.length === 0) return <OnboardingHero />;

  return (
    <div className="flex h-full min-h-0 flex-col">
      {!onboarding && <ContextBar />}
      <div ref={logRef} className={onboarding ? "flex flex-1 flex-col justify-center overflow-hidden px-4 py-4" : "flex-1 space-y-3 overflow-y-auto px-4 py-4"}>
        {!onboarding && <WorkspaceTips />}
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
            if (it.kind === "analysis") {
              // While the live report awaits the user's 3s confirmation (or is
              // being edited), render the interactive AnalysisBlock; otherwise the
              // plain collapsible ReasoningBlock suffices (streaming / settled).
              if (it.confirming || it.editing || it.reanalyzing)
                return (
                  <AnalysisBlock
                    key={it.id}
                    text={it.text}
                    collapsed={it.collapsed}
                    done={it.done}
                    confirming={!!it.confirming}
                    confirmed={!!it.confirmed}
                    secondsLeft={it.secondsLeft ?? 0}
                    editing={!!it.editing}
                    reanalyzing={!!it.reanalyzing}
                    onToggle={() => collapseAnalysisItem(it.id)}
                    onEdit={() => editSummary(it.id)}
                    onSubmit={(text, edited) => submitSummaryConfirm(it.id, text, edited)}
                    onReanalyze={() => reanalyzeSummary(it.id)}
                  />
                );
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
            }
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
            if (it.kind === "adapt_pipeline") return <AdaptPipeline key={it.id} item={it} />;
            if (it.kind === "variants_group") return <VariantsGroup key={it.id} item={it} />;
            if (it.kind === "plan") return <PlanCard key={it.id} item={it} />;
            if (it.kind === "copy")
              return (
                <CopyCard
                  key={it.id}
                  title={it.title}
                  slogans={it.slogans}
                  sellingPoints={it.sellingPoints}
                  platformCopy={it.platformCopy}
                />
              );
            return <ToolCard key={it.id} tool={it.tool} />;
          })}
        </AnimatePresence>
      </div>
      <Composer onboarding={onboarding} />
    </div>
  );
}
