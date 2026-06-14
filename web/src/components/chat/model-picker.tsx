import * as React from "react";
import { useApp } from "@/store/context";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { VendorIcon } from "@/components/vendor-icons";
import { cn } from "@/lib/utils";
import type { ModelEntry } from "@/lib/types";

// Scene display order + labels, matching the backend's four switchable scenes.
const SCENES: { key: string; label: string; hint: string }[] = [
  { key: "chat", label: "逻辑推理", hint: "主对话 / 意图理解" },
  { key: "image", label: "图生图", hint: "换角色 / 背景 / 文案" },
  { key: "text_to_image", label: "文生图", hint: "纯文字生成图片" },
  { key: "video", label: "图生视频", hint: "单图 + 动作描述" },
];

export function ModelPicker({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { state, loadModels, switchModel } = useApp();

  React.useEffect(() => {
    if (open) void loadModels();
  }, [open, loadModels]);

  const catalog = state.models?.catalog ?? {};
  const selected = state.models?.selected ?? {};
  const defaults = state.models?.defaults ?? {};

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>模型设置</DialogTitle>
          <DialogDescription>为每个场景选择模型。切换只影响你的会话与之后的新任务，进行中的任务不受影响。</DialogDescription>
        </DialogHeader>
        <div className="mt-2 space-y-5 max-h-[60vh] overflow-y-auto pr-1">
          {SCENES.map((scene) => {
            const models = catalog[scene.key] ?? [];
            return (
              <section key={scene.key}>
                <div className="mb-2 flex items-baseline gap-2">
                  <h3 className="text-sm font-semibold tracking-tight text-fg">{scene.label}</h3>
                  <span className="text-xs text-fg-mute">{scene.hint}</span>
                </div>
                {models.length === 0 ? (
                  <p className="rounded-md bg-bg-elev/50 px-3 py-2 text-xs text-fg-mute">该场景暂无已配置的模型</p>
                ) : (
                  <div className="grid grid-cols-2 gap-2">
                    {models.map((m: ModelEntry) => {
                      // The effective choice: the session's selection, or the
                      // server default when the session has not chosen one.
                      const isDefault = defaults[scene.key] === m.id;
                      const effective = selected[scene.key] ?? defaults[scene.key];
                      const active = effective === m.id;
                      return (
                        <button
                          key={m.id}
                          type="button"
                          onClick={() => void switchModel(scene.key, m.id)}
                          className={cn(
                            "flex items-center gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-all duration-200 ease-out",
                            active
                              ? "border-accent/60 bg-accent/10 text-fg"
                              : "border-line bg-bg-elev/40 text-fg-dim hover:border-accent/30 hover:bg-bg-elev/70",
                          )}
                        >
                          <span className={cn("grid size-7 shrink-0 place-items-center rounded-md", active ? "text-accent" : "text-fg-mute")}>
                            <VendorIcon iconKey={m.iconKey} className="size-5" />
                          </span>
                          <span className="min-w-0 flex-1">
                            <span className="flex items-center gap-1.5">
                              <span className="truncate text-[13px] font-medium leading-tight">{m.displayName}</span>
                              {isDefault && (
                                <span className="shrink-0 rounded-full border border-line px-1.5 py-px text-[10px] leading-none text-fg-mute">默认</span>
                              )}
                            </span>
                            <span className="block truncate text-[11px] text-fg-mute">{m.vendor}</span>
                          </span>
                        </button>
                      );
                    })}
                  </div>
                )}
              </section>
            );
          })}
        </div>
      </DialogContent>
    </Dialog>
  );
}
