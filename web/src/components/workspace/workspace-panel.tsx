import * as React from "react";
import { AnimatePresence } from "framer-motion";
import { Download, Trash2, CheckCheck, Crop } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { BrandMark } from "@/components/brand-mark";
import { AssetCard } from "./asset-card";
import { TaskCard } from "./task-card";
import { Lightbox } from "./lightbox";
import { SizePicker } from "./size-picker";
import * as api from "@/lib/api";

function Stage({ title, count, action, children }: {
  title: string; count: number; action?: React.ReactNode; children: React.ReactNode;
}) {
  if (count === 0) return null;
  return (
    <section className="mb-5">
      <div className="sticky top-0 z-10 mb-2.5 flex items-center gap-2 bg-bg/85 py-1 backdrop-blur">
        <span className="text-xs font-semibold tracking-wide text-fg-dim">{title}</span>
        <span className="grid h-[18px] min-w-[18px] place-items-center rounded-full border border-line px-1.5 text-[11px] text-fg-mute">
          {count}
        </span>
        {action && <div className="ml-auto">{action}</div>}
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3.5">{children}</div>
    </section>
  );
}

export function WorkspacePanel() {
  const app = useApp();
  const { state } = app;
  const [preview, setPreview] = React.useState<Asset | null>(null);
  const [cropFor, setCropFor] = React.useState<string[] | null>(null);

  const active = [...state.tasks.values()].filter((t) => t.status === "queued" || t.status === "running");
  const failed = [...state.tasks.values()].filter((t) => t.status === "failed");
  const assets = orderedAssets(state.assets, state.order);
  const total = active.length + failed.length + assets.length;

  const allSelected = state.assets.size > 0 && state.selected.size >= state.assets.size;

  const downloadZip = async () => {
    try {
      const blob = await api.downloadZip(state.sessionId, [...state.selected.size ? state.selected : new Set(state.assets.keys())]);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url; a.download = "assets.zip"; a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      app.toast("打包失败：" + (e as Error).message);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b border-line px-5 py-3">
        <BrandMark className="size-5 text-accent" />
        <h2 className="text-sm font-semibold">工作区</h2>
        <div className="ml-auto flex items-center gap-1.5">
          {state.assets.size > 0 && (
            <Button variant="ghost" size="sm" onClick={() => (allSelected ? app.clearSelection() : app.selectAll())}>
              <CheckCheck className="size-3.5" /> {allSelected ? "取消全选" : "全选"}
            </Button>
          )}
          {state.selected.size > 0 && (
            <Button variant="ghost" size="sm" onClick={() => setCropFor([...state.selected])}>
              <Crop className="size-3.5" /> 批量切尺寸
            </Button>
          )}
          {state.assets.size > 0 && (
            <Button variant="ghost" size="sm" onClick={() => { if (confirm("确定清空工作区？将删除全部素材，此操作不可恢复。")) app.clearWorkspace(); }}>
              <Trash2 className="size-3.5" /> 清空
            </Button>
          )}
          {state.assets.size > 0 && (
            <Button size="sm" onClick={downloadZip}>
              <Download className="size-3.5" /> 打包下载
            </Button>
          )}
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 py-4">
        {total === 0 ? (
          <div className="grid h-full place-items-center text-center">
            <div className="max-w-xs text-[13px] leading-relaxed text-fg-mute">
              还没有素材。上传一张图或直接描述你的需求，产物会出现在这里。
            </div>
          </div>
        ) : (
          <>
            <Stage title="进行中" count={active.length}>
              <AnimatePresence initial={false}>
                {active.map((t) => <TaskCard key={t.id} task={t} />)}
              </AnimatePresence>
            </Stage>
            <Stage title="已完成" count={assets.length}>
              <AnimatePresence initial={false}>
                {assets.map((a) => (
                  <AssetCard
                    key={a.id}
                    asset={a}
                    onPreview={setPreview}
                    onCrop={(x) => setCropFor([x.id])}
                    onVideo={(x) => { setPreview(x); }}
                  />
                ))}
              </AnimatePresence>
            </Stage>
            <Stage
              title="失败"
              count={failed.length}
              action={
                <Button variant="ghost" size="xs" onClick={app.clearFailed}>清除全部</Button>
              }
            >
              <AnimatePresence initial={false}>
                {failed.map((t) => <TaskCard key={t.id} task={t} />)}
              </AnimatePresence>
            </Stage>
          </>
        )}
      </div>

      <Lightbox asset={preview} onOpenChange={(o) => !o && setPreview(null)} onCrop={(a) => { setPreview(null); setCropFor([a.id]); }} />
      <SizePicker assetIds={cropFor} onOpenChange={(o) => !o && setCropFor(null)} />
    </div>
  );
}

// orderedAssets honors the user's drag order for ids still present, appending
// any new ids in backend order (newest-first).
function orderedAssets(map: Map<string, Asset>, order: string[]): Asset[] {
  const seen = new Set<string>();
  const out: Asset[] = [];
  for (const id of order) {
    const a = map.get(id);
    if (a) { out.push(a); seen.add(id); }
  }
  for (const a of map.values()) if (!seen.has(a.id)) out.push(a);
  return out;
}
