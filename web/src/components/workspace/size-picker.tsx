import * as React from "react";
import type { Channel, SizePreset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import * as api from "@/lib/api";
import { cn } from "@/lib/utils";

interface Chosen {
  id: string;
  label: string;
}

type CropMode = "cover" | "contain" | "anchor" | "rect";

const MODES: { id: CropMode; name: string; hint: string }[] = [
  { id: "cover", name: "智能铺满", hint: "等比铺满目标框，居中裁掉溢出部分（默认）" },
  { id: "contain", name: "等比留白", hint: "完整放入目标框，多余区域留白，不裁切任何内容" },
  { id: "anchor", name: "九宫格锚点", hint: "铺满后向所选方位裁切，保留该侧主体" },
  { id: "rect", name: "手动框选", hint: "在源图上拖拽框选区域，再缩放到各目标尺寸" },
];

const ANCHORS: { id: string; row: number; col: number }[] = [
  { id: "top-left", row: 0, col: 0 }, { id: "top", row: 0, col: 1 }, { id: "top-right", row: 0, col: 2 },
  { id: "left", row: 1, col: 0 }, { id: "center", row: 1, col: 1 }, { id: "right", row: 1, col: 2 },
  { id: "bottom-left", row: 2, col: 0 }, { id: "bottom", row: 2, col: 1 }, { id: "bottom-right", row: 2, col: 2 },
];

// SizePicker selects platform crop sizes (channel → asset type → size) and runs
// the crop against one or many source assets. Group tabs filter channels; a crop
// mode (cover/contain/anchor/rect) is applied uniformly to all chosen sizes.
export function SizePicker({
  assetIds,
  onOpenChange,
}: {
  assetIds: string[] | null;
  onOpenChange: (open: boolean) => void;
}) {
  const app = useApp();
  const [channels, setChannels] = React.useState<Channel[]>([]);
  const [group, setGroup] = React.useState("all");
  const [activeChannel, setActiveChannel] = React.useState<string | null>(null);
  const [chosen, setChosen] = React.useState<Map<string, Chosen>>(new Map());
  const [running, setRunning] = React.useState(false);
  const [mode, setMode] = React.useState<CropMode>("cover");
  const [anchor, setAnchor] = React.useState("center");
  const [rect, setRect] = React.useState<api.CropRect | null>(null);

  React.useEffect(() => {
    api.listPlatforms().then(setChannels).catch(() => setChannels([]));
  }, []);

  React.useEffect(() => {
    if (assetIds) {
      setChosen(new Map());
      setGroup("all");
      setMode("cover");
      setAnchor("center");
      setRect(null);
    }
  }, [assetIds]);

  const groups = React.useMemo(() => {
    const set = new Set<string>();
    for (const c of channels) if (c.group) set.add(c.group);
    return ["all", ...set];
  }, [channels]);

  const visible = React.useMemo(
    () => channels.filter((c) => group === "all" || c.group === group),
    [channels, group],
  );

  const channel = visible.find((c) => c.id === activeChannel) || visible[0];

  // rect mode operates on a single reference source; with multiple assets the
  // same normalized region is reused, which only makes sense from one preview.
  const multi = !!assetIds && assetIds.length > 1;
  const rectDisabled = multi;
  const sourceAsset = assetIds && assetIds.length ? app.state.assets.get(assetIds[0]) : undefined;

  const toggleSize = (sz: SizePreset, ch: Channel) => {
    setChosen((prev) => {
      const next = new Map(prev);
      if (next.has(sz.id)) next.delete(sz.id);
      else next.set(sz.id, { id: sz.id, label: `${ch.name} · ${sz.name}` });
      return next;
    });
  };

  const cropOpts = (): api.CropOptions => {
    if (mode === "anchor") return { mode, anchor };
    if (mode === "rect" && rect) return { mode, rect };
    if (mode === "contain") return { mode };
    return { mode: "cover" };
  };

  const run = async () => {
    if (!assetIds || chosen.size === 0) return;
    if (mode === "rect" && !rect) {
      app.toast("请先在源图上框选裁剪区域", "warn");
      return;
    }
    setRunning(true);
    const sizeIds = [...chosen.keys()];
    const opts = cropOpts();
    try {
      for (const aid of assetIds) {
        await api.crop(app.state.sessionId, aid, sizeIds, app.state.lossless, opts);
      }
      onOpenChange(false);
      await app.refreshWorkspace(app.state.sessionId);
      app.toast(`已切 ${sizeIds.length} 个尺寸 × ${assetIds.length} 张`, "ok");
    } catch (e) {
      app.toast("切尺寸失败：" + (e as Error).message);
    } finally {
      setRunning(false);
    }
  };

  return (
    <Dialog open={!!assetIds} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(760px,94vw)]">
        <DialogHeader>
          <DialogTitle>
            选择平台尺寸{assetIds && assetIds.length > 1 ? ` · ${assetIds.length} 张` : ""}
          </DialogTitle>
        </DialogHeader>

        {/* crop mode selector */}
        <div className="space-y-2">
          <div className="flex flex-wrap gap-1.5">
            {MODES.map((m) => {
              const disabled = m.id === "rect" && rectDisabled;
              const active = mode === m.id;
              return (
                <button
                  key={m.id}
                  disabled={disabled}
                  onClick={() => setMode(m.id)}
                  className={cn(
                    "rounded-md border px-2.5 py-1 text-xs transition-all duration-200 ease-out",
                    active
                      ? "border-accent bg-accent/15 text-accent"
                      : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
                    disabled && "cursor-not-allowed opacity-40 hover:border-line hover:text-fg-dim",
                  )}
                  title={disabled ? "多张时不支持手动框选" : m.hint}
                >
                  {m.name}
                </button>
              );
            })}
          </div>
          <p className="text-[11px] leading-relaxed text-fg-mute">
            {MODES.find((m) => m.id === mode)?.hint}
          </p>
        </div>

        {/* mode-specific controls */}
        {mode === "anchor" && (
          <AnchorGrid value={anchor} onChange={setAnchor} />
        )}
        {mode === "rect" && (
          <RectSelector src={sourceAsset?.url} rect={rect} onChange={setRect} />
        )}

        <Tabs value={group} onValueChange={setGroup}>
          <TabsList>
            {groups.map((g) => (
              <TabsTrigger key={g} value={g}>{g === "all" ? "全部" : g}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        <div className="mt-1 grid grid-cols-[180px_1fr] gap-3">
          <div className="max-h-[300px] space-y-0.5 overflow-y-auto border-r border-line pr-2">
            {visible.map((c) => {
              const n = countChosen(c, chosen);
              return (
                <button
                  key={c.id}
                  onClick={() => setActiveChannel(c.id)}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[13px] transition-colors",
                    channel?.id === c.id ? "bg-bg-elev-2 text-fg" : "text-fg-dim hover:bg-bg-elev",
                  )}
                >
                  <span className="truncate">{c.name}</span>
                  {n > 0 && <span className="ml-auto rounded-full bg-accent/20 px-1.5 text-[10px] text-accent">{n}</span>}
                </button>
              );
            })}
          </div>

          <div className="max-h-[300px] overflow-y-auto pr-1">
            {channel?.assetTypes.map((at) => {
              const sizes = at.sizes.filter((s) => s.producible);
              if (sizes.length === 0) return null;
              return (
                <div key={at.type} className="mb-4">
                  <div className="mb-2 text-xs font-medium text-fg-mute">{at.name}</div>
                  <div className="flex flex-wrap gap-1.5">
                    {sizes.map((sz) => (
                      <button
                        key={sz.id}
                        onClick={() => toggleSize(sz, channel)}
                        className={cn(
                          "rounded-md border px-2.5 py-1 text-xs transition-colors",
                          chosen.has(sz.id)
                            ? "border-accent bg-accent/15 text-accent"
                            : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
                        )}
                      >
                        {sz.name} <span className="tabular-nums text-fg-mute">{sz.width}×{sz.height}</span>
                      </button>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        <div className="mt-3 flex items-center gap-3 border-t border-line pt-3">
          <span className="text-xs text-fg-dim">已选 {chosen.size} 个尺寸</span>
          <Button className="ml-auto" size="sm" disabled={chosen.size === 0 || running} onClick={run}>
            {running ? "处理中…" : "开始裁剪"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// AnchorGrid is a 3×3 nine-grid position picker for mode=anchor.
function AnchorGrid({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="mb-1.5 flex items-center gap-3">
      <div className="grid grid-cols-3 gap-1" style={{ width: 96 }}>
        {ANCHORS.map((a) => (
          <button
            key={a.id}
            onClick={() => onChange(a.id)}
            aria-label={a.id}
            className={cn(
              "grid aspect-square place-items-center rounded-sm border transition-all duration-200 ease-out",
              value === a.id ? "border-accent bg-accent/20" : "border-line hover:border-accent/50",
            )}
          >
            <span className={cn("size-1.5 rounded-full", value === a.id ? "bg-accent" : "bg-fg-mute")} />
          </button>
        ))}
      </div>
      <p className="text-[11px] leading-relaxed text-fg-mute">
        裁切时优先保留所选方位的画面。例如选「上」会保留顶部、裁掉底部。
      </p>
    </div>
  );
}

// RectSelector lets the user drag a normalized crop region on the source image.
// All coordinates are stored as fractions ([0,1]) so they apply to any product
// size. Pointer events are tracked relative to the rendered image box.
function RectSelector({
  src,
  rect,
  onChange,
}: {
  src?: string;
  rect: api.CropRect | null;
  onChange: (r: api.CropRect | null) => void;
}) {
  const boxRef = React.useRef<HTMLDivElement>(null);
  const dragRef = React.useRef<{ startX: number; startY: number } | null>(null);

  if (!src) {
    return <p className="text-[11px] text-fg-mute">源图不可用，无法框选。</p>;
  }

  const clamp = (v: number) => Math.min(1, Math.max(0, v));

  const fracFromEvent = (e: React.PointerEvent) => {
    const el = boxRef.current;
    if (!el) return { x: 0, y: 0 };
    const r = el.getBoundingClientRect();
    return {
      x: clamp((e.clientX - r.left) / r.width),
      y: clamp((e.clientY - r.top) / r.height),
    };
  };

  const onPointerDown = (e: React.PointerEvent) => {
    e.preventDefault();
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    const p = fracFromEvent(e);
    dragRef.current = { startX: p.x, startY: p.y };
    onChange({ x: p.x, y: p.y, w: 0, h: 0 });
  };

  const onPointerMove = (e: React.PointerEvent) => {
    if (!dragRef.current) return;
    const p = fracFromEvent(e);
    const { startX, startY } = dragRef.current;
    onChange({
      x: Math.min(startX, p.x),
      y: Math.min(startY, p.y),
      w: Math.abs(p.x - startX),
      h: Math.abs(p.y - startY),
    });
  };

  const onPointerUp = (e: React.PointerEvent) => {
    if (dragRef.current) {
      const r = rect;
      // discard a near-zero drag (treated as a click, not a selection).
      if (r && (r.w < 0.02 || r.h < 0.02)) onChange(null);
    }
    dragRef.current = null;
    (e.target as HTMLElement).releasePointerCapture?.(e.pointerId);
  };

  return (
    <div className="space-y-1.5">
      <div
        ref={boxRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        className="relative mx-auto max-h-[260px] w-fit cursor-crosshair touch-none select-none overflow-hidden rounded-md border border-line bg-bg"
      >
        <img src={src} alt="框选源图" draggable={false} className="max-h-[260px] w-auto object-contain" />
        {rect && rect.w > 0 && rect.h > 0 && (
          <>
            <div className="pointer-events-none absolute inset-0 bg-black/45" />
            <div
              className="pointer-events-none absolute border-2 border-accent bg-transparent shadow-[0_0_0_9999px_rgba(0,0,0,0.45)]"
              style={{
                left: `${rect.x * 100}%`,
                top: `${rect.y * 100}%`,
                width: `${rect.w * 100}%`,
                height: `${rect.h * 100}%`,
              }}
            />
          </>
        )}
      </div>
      <div className="flex items-center justify-between text-[11px] text-fg-mute">
        <span>在图上拖拽框选裁剪区域</span>
        {rect && rect.w > 0 && (
          <button className="text-accent hover:underline" onClick={() => onChange(null)}>清除框选</button>
        )}
      </div>
    </div>
  );
}

function countChosen(c: Channel, chosen: Map<string, Chosen>): number {
  let n = 0;
  for (const at of c.assetTypes) for (const s of at.sizes) if (chosen.has(s.id)) n++;
  return n;
}
