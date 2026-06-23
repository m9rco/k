import * as React from "react";
import { Layers, Trash2, ArrowUp, ArrowDown, Download, Loader2 } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { persistComposite, layerSplit, type SplitLayer } from "@/lib/api";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import * as VisuallyHidden from "@radix-ui/react-visually-hidden";

// A Layer is one placed image on the compositing canvas. x/y are canvas-space
// pixels (full resolution); scale is uniform. The background layer is locked
// (role==="background") — it stays at the origin, full size, bottom of the stack,
// and cannot be moved, scaled or removed. Subject layers are freely transformable.
// Transforms are non-destructive — the source asset is never modified.
interface Layer {
  uid: string;
  asset: Asset;
  role: "background" | "subject";
  desc?: string;
  x: number;
  y: number;
  scale: number;
}

const MAX_PREVIEW_W = 600;
const MAX_PREVIEW_H = 460;

// CompositingCanvas is the per-image 图层精修 surface. It is opened FOR a specific
// source image (splitFor): on open it runs a layer split — detect foreground
// subjects, cut each onto a transparent layer (Gemini) and inpaint a clean
// background — then stacks the returned layers on a canvas whose size is LOCKED to
// the source image dimensions (不可调整). The user moves / uniformly scales /
// reorders / removes the SUBJECT layers over the locked background, then flattens
// to a PNG that lands in the workspace. Pure browser compositing on export — no
// generation cost beyond the initial split.
export function CompositingCanvas({
  splitFor,
  onOpenChange,
}: {
  splitFor: Asset | null;
  onOpenChange: (open: boolean) => void;
}) {
  const app = useApp();
  const [layers, setLayers] = React.useState<Layer[]>([]);
  const [canvasW, setCanvasW] = React.useState(0);
  const [canvasH, setCanvasH] = React.useState(0);
  const [selUid, setSelUid] = React.useState<string | null>(null);
  const [splitting, setSplitting] = React.useState(false);
  const [exporting, setExporting] = React.useState(false);
  const drag = React.useRef<{ uid: string; dx: number; dy: number } | null>(null);
  const uidCounter = React.useRef(0);
  // assetById resolves a split layer's asset id to its full Asset (url/dims).
  const assetById = React.useCallback((id: string) => app.state.assets.get(id), [app.state.assets]);

  const open = !!splitFor;

  React.useEffect(() => {
    if (!open) {
      setLayers([]);
      setCanvasW(0);
      setCanvasH(0);
      setSelUid(null);
      setSplitting(false);
      return;
    }
    // Run the split for this source image, then build the layer stack.
    let cancelled = false;
    setLayers([]);
    setSplitting(true);
    (async () => {
      try {
        const res = await layerSplit(app.state.sessionId, splitFor!.id);
        // The split products land in the workspace; refresh so assetById resolves them.
        await app.refreshWorkspace(app.state.sessionId);
        if (cancelled) return;
        setCanvasW(res.width || splitFor!.width || 0);
        setCanvasH(res.height || splitFor!.height || 0);
        const built: Layer[] = res.layers.map((l: SplitLayer) => ({
          uid: `L${uidCounter.current++}`,
          // Resolve from the freshly-refreshed map; fall back to a minimal asset so
          // the layer still renders even if the map hasn't propagated yet.
          asset: app.state.assets.get(l.assetId) || ({ id: l.assetId, kind: "composite", url: `/api/session/${app.state.sessionId}/assets/${l.assetId}/download`, width: res.width, height: res.height } as Asset),
          role: l.role,
          desc: l.desc,
          x: 0,
          y: 0,
          scale: 1,
        }));
        setLayers(built);
      } catch (e) {
        if (!cancelled) {
          app.toast("图层精修失败：" + (e as Error).message);
          onOpenChange(false);
        }
      } finally {
        if (!cancelled) setSplitting(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, splitFor?.id]);

  const dispScale = canvasW > 0 ? Math.min(MAX_PREVIEW_W / canvasW, MAX_PREVIEW_H / canvasH, 1) : 1;

  const removeLayer = (uid: string) => {
    setLayers((prev) => prev.filter((l) => l.uid !== uid || l.role === "background"));
    setSelUid((s) => (s === uid ? null : s));
  };

  const moveZ = (uid: string, dir: 1 | -1) => {
    setLayers((prev) => {
      const i = prev.findIndex((l) => l.uid === uid);
      const j = i + dir;
      // Never move below the background (index 0) or out of bounds.
      if (i <= 0 || j <= 0 || j >= prev.length) return prev;
      const next = [...prev];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  };

  const setScale = (uid: string, scale: number) =>
    setLayers((prev) => prev.map((l) => (l.uid === uid && l.role !== "background" ? { ...l, scale } : l)));

  const onPointerDown = (e: React.PointerEvent, l: Layer) => {
    if (l.role === "background") return; // background is locked
    e.preventDefault();
    setSelUid(l.uid);
    drag.current = { uid: l.uid, dx: e.clientX - l.x * dispScale, dy: e.clientY - l.y * dispScale };
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    const x = (e.clientX - d.dx) / dispScale;
    const y = (e.clientY - d.dy) / dispScale;
    setLayers((prev) => prev.map((l) => (l.uid === d.uid ? { ...l, x, y } : l)));
  };
  const onPointerUp = () => {
    drag.current = null;
  };

  const loadImage = (url: string) =>
    new Promise<HTMLImageElement>((resolve, reject) => {
      const img = new Image();
      img.crossOrigin = "anonymous";
      img.onload = () => resolve(img);
      img.onerror = reject;
      img.src = url;
    });

  const exportComposite = async () => {
    if (layers.length === 0 || canvasW === 0) return;
    setExporting(true);
    try {
      const cv = document.createElement("canvas");
      // Export at the LOCKED canvas size (= source image size, e.g. 900×500) —
      // never the scaled-down preview size. Layers are server-converged to the
      // source dimensions, so each draws 1:1 at full resolution (no compression).
      cv.width = canvasW;
      cv.height = canvasH;
      const ctx = cv.getContext("2d");
      if (!ctx) throw new Error("canvas 2d context unavailable");
      for (const l of layers) {
        const a = assetById(l.asset.id) || l.asset;
        const img = await loadImage(a.url);
        // Use the loaded bitmap's natural pixels as the source of truth for the
        // layer's intrinsic size, then apply the user's uniform scale.
        const w = img.naturalWidth * l.scale;
        const h = img.naturalHeight * l.scale;
        ctx.drawImage(img, l.x, l.y, w, h);
      }
      const blob: Blob = await new Promise((resolve, reject) =>
        cv.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob failed"))), "image/png"),
      );
      const sources = [...new Set(layers.map((l) => l.asset.id))];
      await persistComposite(app.state.sessionId, blob, sources);
      await app.refreshWorkspace(app.state.sessionId);
      app.toast("已导出合成图到工作区", "ok");
      onOpenChange(false);
    } catch (e) {
      app.toast("导出失败：" + (e as Error).message);
    } finally {
      setExporting(false);
    }
  };

  const selected = layers.find((l) => l.uid === selUid) || null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(960px,96vw)]">
        <VisuallyHidden.Root>
          <DialogTitle>图层精修拼接画布</DialogTitle>
        </VisuallyHidden.Root>
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <Layers className="size-4 text-accent" />
            <h3 className="text-sm font-semibold tracking-tight">图层精修</h3>
            <span className="text-[11px] text-fg-mute">
              {canvasW > 0 ? `画布 ${canvasW}×${canvasH}（锁定为原图尺寸）` : "正在分析原图…"}
            </span>
            <div className="ml-auto">
              <Button
                size="sm"
                onClick={exportComposite}
                disabled={layers.length === 0 || exporting || splitting}
              >
                <Download className="size-3.5" /> {exporting ? "导出中…" : "导出合成图"}
              </Button>
            </div>
          </div>

          {splitting ? (
            <div className="grid h-64 place-items-center rounded-md border border-line">
              <div className="flex flex-col items-center gap-2 text-fg-mute">
                <Loader2 className="size-5 animate-spin text-accent" />
                <span className="text-[12px]">正在分析原图并分割图层（抠主体 + 补全背景）…</span>
              </div>
            </div>
          ) : (
            <div className="flex gap-4">
              {/* Canvas surface — fixed to the source image size */}
              <div className="flex-1">
                <div
                  className="relative mx-auto overflow-hidden rounded-md border border-line"
                  style={{
                    width: canvasW * dispScale || MAX_PREVIEW_W,
                    height: canvasH * dispScale || 300,
                    // Checkerboard so transparent layer areas read clearly.
                    backgroundColor: "#fff",
                    backgroundImage:
                      "linear-gradient(45deg,#0000000d 25%,transparent 25%,transparent 75%,#0000000d 75%),linear-gradient(45deg,#0000000d 25%,transparent 25%,transparent 75%,#0000000d 75%)",
                    backgroundSize: "16px 16px",
                    backgroundPosition: "0 0,8px 8px",
                  }}
                  onPointerMove={onPointerMove}
                  onPointerUp={onPointerUp}
                >
                  {layers.map((l) => {
                    const a = assetById(l.asset.id) || l.asset;
                    return (
                      <img
                        key={l.uid}
                        src={a.url}
                        alt={l.role === "background" ? "背景" : l.desc || "图层"}
                        draggable={false}
                        onPointerDown={(e) => onPointerDown(e, l)}
                        className={cn(
                          "absolute select-none transition-shadow duration-200 ease-out",
                          l.role === "background" ? "cursor-default" : "cursor-move",
                          selUid === l.uid && l.role !== "background" && "outline outline-2 outline-accent",
                        )}
                        style={{
                          left: l.x * dispScale,
                          top: l.y * dispScale,
                          width: (a.width || canvasW) * l.scale * dispScale,
                          height: (a.height || canvasH) * l.scale * dispScale,
                        }}
                      />
                    );
                  })}
                </div>
              </div>

              {/* Layer panel */}
              <div className="w-56 shrink-0 space-y-2">
                <div className="text-[11px] font-medium text-fg-mute">图层（顶层在上）</div>
                <div className="space-y-1.5">
                  {[...layers].reverse().map((l) => {
                    const a = assetById(l.asset.id) || l.asset;
                    const isBg = l.role === "background";
                    return (
                      <div
                        key={l.uid}
                        onClick={() => !isBg && setSelUid(l.uid)}
                        className={cn(
                          "flex items-center gap-2 rounded-md border p-1.5 transition-all duration-200 ease-out",
                          selUid === l.uid && !isBg ? "border-accent/60 bg-accent/5" : "border-line",
                          isBg ? "opacity-80" : "hover:border-line-strong cursor-pointer",
                        )}
                      >
                        <img src={a.url} alt="" className="size-8 rounded object-cover" />
                        <span className="truncate text-[11px] text-fg-dim">
                          {isBg ? "背景（锁定）" : l.desc || "主体"}
                        </span>
                        {!isBg && (
                          <div className="ml-auto flex items-center gap-0.5">
                            <button type="button" title="上移" onClick={(e) => { e.stopPropagation(); moveZ(l.uid, 1); }} className="grid size-6 place-items-center rounded text-fg-mute transition-colors hover:text-fg">
                              <ArrowUp className="size-3.5" />
                            </button>
                            <button type="button" title="下移" onClick={(e) => { e.stopPropagation(); moveZ(l.uid, -1); }} className="grid size-6 place-items-center rounded text-fg-mute transition-colors hover:text-fg">
                              <ArrowDown className="size-3.5" />
                            </button>
                            <button type="button" title="移除" onClick={(e) => { e.stopPropagation(); removeLayer(l.uid); }} className="grid size-6 place-items-center rounded text-fg-mute transition-colors hover:text-red-500">
                              <Trash2 className="size-3.5" />
                            </button>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>

                {selected && selected.role !== "background" && (
                  <div className="space-y-1 border-t border-line pt-2">
                    <label className="text-[11px] text-fg-mute">缩放 {Math.round(selected.scale * 100)}%</label>
                    <input
                      type="range"
                      min={0.1}
                      max={3}
                      step={0.01}
                      value={selected.scale}
                      onChange={(e) => setScale(selected.uid, Number(e.target.value))}
                      className="w-full accent-accent"
                    />
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
