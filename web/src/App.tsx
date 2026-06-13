import { motion } from "framer-motion";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ToastHost } from "@/components/toast-host";
import { AppProvider, useApp } from "@/store/context";
import { ChatPanel } from "@/components/chat/chat-panel";
import { WorkspacePanel } from "@/components/workspace/workspace-panel";

// Shell decides between the immersive onboarding layout (chat centered, no
// workspace) and the working layout (7:3 split). The switch is driven purely by
// whether the workspace holds anything, and animated with a layout transition.
function Shell() {
  const { state } = useApp();
  // Immersive onboarding when there is nothing to show: no assets and no
  // active/failed tasks. (Historical "done" tasks without assets don't count —
  // they would otherwise trap the UI out of the empty state.)
  const hasLiveTask = [...state.tasks.values()].some(
    (t) => t.status === "queued" || t.status === "running" || t.status === "failed",
  );
  const isEmpty = state.assets.size === 0 && !hasLiveTask;

  if (isEmpty) {
    return (
      <div className="grid h-full place-items-center px-4">
        <motion.div
          layout
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4, ease: "easeOut" }}
          className="flex h-full max-h-[820px] w-full max-w-[600px] flex-col"
        >
          <ChatPanel onboarding />
        </motion.div>
      </div>
    );
  }

  return (
    <div className="grid h-full grid-cols-1 lg:grid-cols-[7fr_3fr]">
      {/* 工作区在左、占主区 */}
      <motion.main
        layout
        initial={{ opacity: 0, x: -24 }}
        animate={{ opacity: 1, x: 0 }}
        transition={{ duration: 0.4, ease: "easeOut" }}
        className="order-2 min-h-0 border-line lg:order-1 lg:border-r"
      >
        <WorkspacePanel />
      </motion.main>
      {/* 对话区在右 */}
      <motion.aside layout transition={{ duration: 0.4, ease: "easeOut" }} className="order-1 min-h-0 lg:order-2">
        <ChatPanel />
      </motion.aside>
    </div>
  );
}

export function App() {
  return (
    <ToastHost>
      <TooltipProvider delayDuration={300}>
        <AppProvider>
          <Shell />
        </AppProvider>
      </TooltipProvider>
    </ToastHost>
  );
}
