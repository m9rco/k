import { TooltipProvider } from "@/components/ui/tooltip";
import { ToastHost } from "@/components/toast-host";
import { AppProvider } from "@/store/context";
import { ChatPanel } from "@/components/chat/chat-panel";
import { WorkspacePanel } from "@/components/workspace/workspace-panel";

export function App() {
  return (
    <ToastHost>
      <TooltipProvider delayDuration={300}>
        <AppProvider>
          <div className="grid h-full grid-cols-1 lg:grid-cols-[7fr_3fr]">
            {/* 工作区在左、占主区 */}
            <main className="order-2 min-h-0 border-line lg:order-1 lg:border-r">
              <WorkspacePanel />
            </main>
            {/* 对话区在右 */}
            <aside className="order-1 min-h-0 lg:order-2">
              <ChatPanel />
            </aside>
          </div>
        </AppProvider>
      </TooltipProvider>
    </ToastHost>
  );
}
