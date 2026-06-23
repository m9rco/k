import * as React from "react";
import { Download, Trash2, CheckCheck, LayoutGrid, GitCommitVertical, Stamp } from "lucide-react";
import type { Asset } from "@/lib/types";
import { StampAlbum } from "./stamp-album";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { BrandMark } from "@/components/brand-mark";
import { Timeline } from "./timeline";
import { WorkspaceGrid } from "./workspace-grid";
import { Lightbox } from "./lightbox";
import { SizePicker } from "./size-picker";
import { CompositingCanvas } from "./compositing-canvas";
import { buildTimeline, assetLabels } from "@/lib/timeline";
import { cn } from "@/lib/utils";
import * as api from "@/lib/api";

type ViewMode = "grid" | "timeline" | "stamp";
const VIEW_KEY = "gas_workspace_view";

export function WorkspacePanel() {
  const app = useApp();
  const { state } = app;
  const [preview, setPreview] = React.useState<Asset | null>(null);
  const [cropFor, setCropFor] = React.useState<string[] | null>(null);
  const [splitFor, setSplitFor] = React.useState<Asset | null>(null);
  // View mode: stamp (集邮册/宣发清单, default), grid (show-all), or timeline
  // (production line). Persisted in sessionStorage so a reload keeps the user's
  // choice; absent any stored value we land on the stamp album.
  const [view, setView] = React.useState<ViewMode>(() => {
    const v = typeof sessionStorage !== "undefined" ? sessionStorage.getItem(VIEW_KEY) : null;
    return v === "timeline" ? "timeline" : v === "grid" ? "grid" : "stamp";
  });
  const pickView = (v: ViewMode) => {
    setView(v);
    try { sessionStorage.setItem(VIEW_KEY, v); } catch { /* ignore */ }
  };

  // A coarse clock so relative times ("3 分钟前") refresh and active nodes keep
  // floating to the top. 30s cadence is plenty for minute-granularity labels.
  const [nowMs, setNowMs] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = window.setInterval(() => setNowMs(Date.now()), 30000);
    return () => window.clearInterval(id);
  }, []);

  const assets = React.useMemo(() => [...state.assets.values()], [state.assets]);
  const tasks = React.useMemo(() => [...state.tasks.values()], [state.tasks]);
  const nodes = React.useMemo(() => buildTimeline(assets, tasks, nowMs), [assets, tasks, nowMs]);
  const labels = React.useMemo(() => assetLabels(assets), [assets]);
  // Assets in creation order (earliest first) — matches numbering labels; the
  // grid reverses for newest-first display while keeping the label numbering.
  const assetsByTime = React.useMemo(
    () => [...assets].sort((a, b) => (a.createdAt ? Date.parse(a.createdAt) : 0) - (b.createdAt ? Date.parse(b.createdAt) : 0)),
    [assets],
  );
  const hasFailed = tasks.some((t) => t.status === "failed");
  const isEmpty = nodes.length === 0;

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
        {/* View toggle: grid (all) ↔ timeline (production line) */}
        <div className="ml-3 flex items-center rounded-md border border-line p-0.5">
          <button
            type="button"
            title="全部展示（网格）"
            onClick={() => pickView("grid")}
            className={cn("grid size-6 place-items-center rounded transition-colors", view === "grid" ? "bg-bg-elev-2 text-fg" : "text-fg-mute hover:text-fg")}
          >
            <LayoutGrid className="size-3.5" />
          </button>
          <button
            type="button"
            title="时间轴（加工流水）"
            onClick={() => pickView("timeline")}
            className={cn("grid size-6 place-items-center rounded transition-colors", view === "timeline" ? "bg-bg-elev-2 text-fg" : "text-fg-mute hover:text-fg")}
          >
            <GitCommitVertical className="size-3.5" />
          </button>
          <button
            type="button"
            title="集邮册（宣发清单）"
            onClick={() => pickView("stamp")}
            className={cn("grid size-6 place-items-center rounded transition-colors", view === "stamp" ? "bg-bg-elev-2 text-fg" : "text-fg-mute hover:text-fg")}
          >
            <Stamp className="size-3.5" />
          </button>
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          {state.assets.size > 0 && (
            <Button variant="ghost" size="sm" onClick={() => (allSelected ? app.clearSelection() : app.selectAll())}>
              <CheckCheck className="size-3.5" /> {allSelected ? "取消全选" : "全选"}
            </Button>
          )}
          {state.selected.size > 0 && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => { if (confirm(`确定移除选中的 ${state.selected.size} 张素材？此操作不可恢复。`)) app.removeSelected(); }}
            >
              <Trash2 className="size-3.5" /> 移除 {state.selected.size}
            </Button>
          )}
          {hasFailed && (
            <Button variant="ghost" size="sm" onClick={app.clearFailed}>
              清除失败
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
        {view === "stamp" ? (
          <StampAlbum onPreview={setPreview} />
        ) : isEmpty ? (
          <div className="h-full" />
        ) : view === "timeline" ? (
          <Timeline
            nodes={nodes}
            labels={labels}
            nowMs={nowMs}
            onPreview={setPreview}
            onCrop={(x) => setCropFor([x.id])}
            onVideo={(x) => { setPreview(x); }}
            onLayerSplit={setSplitFor}
          />
        ) : (
          <WorkspaceGrid
            assets={assetsByTime}
            labels={labels}
            onPreview={setPreview}
            onCrop={(x) => setCropFor([x.id])}
            onVideo={(x) => { setPreview(x); }}
            onLayerSplit={setSplitFor}
          />
        )}
      </div>

      <Lightbox asset={preview} onOpenChange={(o) => !o && setPreview(null)} onCrop={(a) => { setPreview(null); setCropFor([a.id]); }} onLayerSplit={(a) => { setPreview(null); setSplitFor(a); }} />
      <SizePicker assetIds={cropFor} onOpenChange={(o) => !o && setCropFor(null)} />
      <CompositingCanvas splitFor={splitFor} onOpenChange={(o) => !o && setSplitFor(null)} />
    </div>
  );
}

