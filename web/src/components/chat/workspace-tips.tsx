import * as React from "react";
import { AnimatePresence, motion } from "framer-motion";
import { Sparkles, X } from "lucide-react";

// CAPABILITIES is the studio's tool surface, phrased as "动作 · 说明" so each
// row reads as one concrete thing the user can ask for.
const CAPABILITIES = [
  "换背景 · 把图片背景替换成你描述的场景，自动做颜色适配",
  "换角色 · 替换图片中的主体，保留整体构图",
  "换文案 · 替换图片上的宣传文字",
  "切尺寸 · 按平台广告位尺寸裁剪，纯裁剪不经过 AI",
  "生视频 · 基于一张图加动作描述生成短视频",
  "搜图 · 按关键词搜索参考图并下载到工作区",
  "宣发文案 · 为游戏写主标题 / 广告语 / 卖点 / 平台投放文案",
  "文字叠加 · 把 CTA / 折扣角标 / 定档大字确定性叠加到图上，文字清晰无错字",
  "批量变体 · 同一张图一次出多个 creative 版本（构图 / 配色 / 风格 / 文案），买量测 CTR",
  "下载 / 打包 · 单张下载或批量打包 zip",
];

const DISMISS_KEY = "gas_workspace_tips_dismissed";

// WorkspaceTips is the gentle hand-off shown the first time the user leaves the
// immersive home for the working (split) layout: it carries forward the old
// Welcome copy as a compact, dismissible "你可以这样用" capability list so the
// onboarding affordance survives the transition without crowding the chat. Once
// closed, sessionStorage keeps it closed for the rest of the session.
export function WorkspaceTips() {
  const [dismissed, setDismissed] = React.useState<boolean>(() => {
    try {
      return sessionStorage.getItem(DISMISS_KEY) === "1";
    } catch {
      return false;
    }
  });

  const dismiss = () => {
    setDismissed(true);
    try {
      sessionStorage.setItem(DISMISS_KEY, "1");
    } catch {
      /* ignore */
    }
  };

  return (
    <AnimatePresence initial={false}>
      {!dismissed && (
        <motion.div
          initial={{ opacity: 0, y: -6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, height: 0, marginBottom: 0 }}
          transition={{ duration: 0.2, ease: "easeOut" }}
          className="relative overflow-hidden rounded-lg border border-accent/30 bg-bg-elev/60 px-4 py-3.5"
        >
          <button
            type="button"
            onClick={dismiss}
            className="absolute right-2.5 top-2.5 text-fg-mute transition-colors hover:text-fg"
            title="关闭"
          >
            <X className="size-3.5" />
          </button>
          <div className="flex items-center gap-1.5">
            <Sparkles className="size-3.5 text-accent" />
            <p className="text-[13px] font-medium text-fg">嗨，我是你的宣发素材助手</p>
          </div>
          <p className="mt-1 text-[12px] leading-relaxed text-fg-dim">上传一张图，告诉我你想做什么：</p>
          <ul className="mt-2.5 space-y-1">
            {CAPABILITIES.map((c) => {
              const [action, ...rest] = c.split(" · ");
              return (
                <li key={c} className="text-[12px] leading-relaxed text-fg-dim">
                  <span className="font-medium text-fg">{action}</span>
                  {rest.length > 0 && <span className="text-fg-mute"> · {rest.join(" · ")}</span>}
                </li>
              );
            })}
          </ul>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
