import * as React from "react";
import { MousePointerClick, Square, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import type { RegionBox } from "@/lib/api";

// RegionSelector overlays an image and lets the user pick ONE region two ways:
//   1. Point mode (default) — click an object; the backend vision model looks at
//      the FULL image + the click point, identifies the object/layer under it,
//      and returns its bounding box + a structured feature description. No
//      browser-side model: the click is just a normalized coordinate sent to the
//      server, so it's instant on the client and never freezes the UI.
//   2. Manual rectangle — drag a box. Always available, the explicit fallback.
//
// It reports the chosen selection via onPoint (a normalized click) or onRect (a
// normalized box). The parent fetches the description and, for point mode, passes
// the located box back via `resultBox` so the overlay snaps to the object.
export function RegionSelector({
  src,
  onPoint,
  onRect,
  busy,
  resultBox,
}: {
  src: string;
  // onPoint fires with a normalized click ∈ [0,1] (point mode).
  onPoint: (px: number, py: number) => void;
  // onRect fires with a normalized box (rect mode).
  onRect: (box: RegionBox) => void;
  busy?: boolean;
  // resultBox is the located object's box (point mode) the parent feeds back so
  // the overlay snaps to what the vision model found.
  resultBox?: RegionBox | null;
}) {
  const [mode, setMode] = React.useState<"point" | "rect">("point");
  const [box, setBox] = React.useState<RegionBox | null>(null);
  const [drag, setDrag] = React.useState<{ x: number; y: number } | null>(null);
  const imgRef = React.useRef<HTMLImageElement>(null);
  const wrapRef = React.useRef<HTMLDivElement>(null);

  // When the parent supplies a located box (point mode result), show it.
  React.useEffect(() => {
    if (resultBox) setBox(resultBox);
  }, [resultBox]);

  // Reset the overlay when the image changes.
  React.useEffect(() => {
    setBox(null);
    setDrag(null);
  }, [src]);

  // Normalize a client point into the IMAGE's content box, not the wrapper. The
  // <img> uses object-contain, so it is letterboxed inside the wrapper when
  // aspect ratios differ. Returns null when the click lands on the letterbox.
  const norm = (clientX: number, clientY: number): { x: number; y: number } | null => {
    const r = imgRef.current?.getBoundingClientRect();
    if (!r || r.width === 0 || r.height === 0) return null;
    const rx = (clientX - r.left) / r.width;
    const ry = (clientY - r.top) / r.height;
    if (rx < 0 || rx > 1 || ry < 0 || ry > 1) return null;
    return { x: rx, y: ry };
  };

  const onClickPoint = (e: React.MouseEvent) => {
    if (mode !== "point" || busy) return;
    const p = norm(e.clientX, e.clientY);
    if (!p) return;
    // Show an instant point marker; the real box arrives via resultBox.
    setBox({ x: p.x, y: p.y, w: 0, h: 0 });
    onPoint(p.x, p.y);
  };

  const onPointerDown = (e: React.MouseEvent) => {
    if (mode !== "rect") return;
    const p = norm(e.clientX, e.clientY);
    if (!p) return;
    setDrag({ x: p.x, y: p.y });
    setBox({ x: p.x, y: p.y, w: 0, h: 0 });
  };
  const onPointerMove = (e: React.MouseEvent) => {
    if (mode !== "rect" || !drag) return;
    const p = norm(e.clientX, e.clientY);
    if (!p) return;
    setBox({
      x: Math.min(drag.x, p.x),
      y: Math.min(drag.y, p.y),
      w: Math.abs(p.x - drag.x),
      h: Math.abs(p.y - drag.y),
    });
  };
  const onPointerUp = () => {
    if (mode !== "rect" || !drag) return;
    setDrag(null);
    if (box && box.w > 0.01 && box.h > 0.01) onRect(box);
  };

  // The bbox is in IMAGE-relative coords. Because the <img> is object-contain it
  // may be letterboxed; compute the image's rect relative to the wrapper so the
  // overlay lines up. Only render when the box has area (point markers w/h=0 are
  // shown as a small dot instead).
  const overlayStyle = React.useMemo(() => {
    if (!box || box.w <= 0 || box.h <= 0) return null;
    const img = imgRef.current;
    const wrap = wrapRef.current;
    if (!img || !wrap) return null;
    const ir = img.getBoundingClientRect();
    const wr = wrap.getBoundingClientRect();
    if (wr.width === 0 || wr.height === 0) return null;
    const offX = (ir.left - wr.left) / wr.width;
    const offY = (ir.top - wr.top) / wr.height;
    const sx = ir.width / wr.width;
    const sy = ir.height / wr.height;
    return {
      left: `${(offX + box.x * sx) * 100}%`,
      top: `${(offY + box.y * sy) * 100}%`,
      width: `${box.w * sx * 100}%`,
      height: `${box.h * sy * 100}%`,
    };
  }, [box]);

  // A dot marker for the click while the box is being located (point mode, no
  // area yet). Positioned in the same image-relative space as overlayStyle.
  const dotStyle = React.useMemo(() => {
    if (!box || box.w > 0 || box.h > 0) return null;
    const img = imgRef.current;
    const wrap = wrapRef.current;
    if (!img || !wrap) return null;
    const ir = img.getBoundingClientRect();
    const wr = wrap.getBoundingClientRect();
    if (wr.width === 0 || wr.height === 0) return null;
    const offX = (ir.left - wr.left) / wr.width;
    const offY = (ir.top - wr.top) / wr.height;
    const sx = ir.width / wr.width;
    const sy = ir.height / wr.height;
    return {
      left: `${(offX + box.x * sx) * 100}%`,
      top: `${(offY + box.y * sy) * 100}%`,
    };
  }, [box]);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => setMode("point")}
          className={cn(
            "inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] transition-all duration-200 ease-out",
            mode === "point"
              ? "border-accent/60 bg-accent/10 text-fg"
              : "border-line text-fg-mute hover:text-fg",
          )}
        >
          <MousePointerClick className="size-3.5" />
          点选物体
        </button>
        <button
          type="button"
          onClick={() => setMode("rect")}
          className={cn(
            "inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[12px] transition-all duration-200 ease-out",
            mode === "rect" ? "border-accent/60 bg-accent/10 text-fg" : "border-line text-fg-mute hover:text-fg",
          )}
        >
          <Square className="size-3.5" /> 框选
        </button>
        <span className="text-[11px] text-fg-mute">
          {mode === "point"
            ? busy
              ? "正在识别点中的物体…"
              : "点击图中的角色/道具/文字，自动识别该图层"
            : "拖拽框出要修改的区域"}
        </span>
      </div>

      <div
        ref={wrapRef}
        className={cn(
          "relative w-full overflow-hidden rounded-md bg-bg",
          mode === "rect" ? "cursor-crosshair" : "cursor-pointer",
          busy && "pointer-events-none",
        )}
        onClick={onClickPoint}
        onMouseDown={onPointerDown}
        onMouseMove={onPointerMove}
        onMouseUp={onPointerUp}
        onMouseLeave={onPointerUp}
      >
        <img
          ref={imgRef}
          src={src}
          alt="选区"
          draggable={false}
          className="max-h-[52vh] w-full select-none object-contain"
        />
        {overlayStyle && (
          <div
            className="pointer-events-none absolute border-2 border-accent bg-accent/15"
            style={overlayStyle}
          />
        )}
        {dotStyle && (
          <div
            className="pointer-events-none absolute size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-accent bg-accent/40"
            style={dotStyle}
          />
        )}
        {busy && (
          <div className="absolute inset-0 grid place-items-center bg-bg/40">
            <Loader2 className="size-5 animate-spin text-accent" />
          </div>
        )}
      </div>
    </div>
  );
}
