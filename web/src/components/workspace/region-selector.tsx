import * as React from "react";
import { Loader2, Sparkles, Square, Lasso, Undo2 } from "lucide-react";
import { cn } from "@/lib/utils";
import type { RegionBox, RegionPoint } from "@/lib/api";

type Mode = "rect" | "poly";

// A staged selection waiting for confirmation. Either a rect (drag a box) or a
// polygon (click vertices). Recognition fires only on confirm.
type Pending =
  | { kind: "rect"; box: RegionBox }
  | { kind: "poly"; points: RegionPoint[] };

// RegionSelector overlays an image and lets the user mark the area to edit two
// ways: drag a rectangle, or click vertices to lasso an irregular polygon.
// Selection and recognition are two steps — drawing only stages the selection
// (dimmed background + animated marching-ants border / outline); the parent's
// describe (识别) fires solely when the user hits the confirm button.
export function RegionSelector({
  src,
  onRect,
  onPoly,
  busy,
}: {
  src: string;
  // onRect fires with a normalized box ∈ [0,1] when a rect is confirmed.
  onRect: (box: RegionBox) => void;
  // onPoly fires with normalized vertices ∈ [0,1] when a polygon is confirmed.
  onPoly: (points: RegionPoint[]) => void;
  busy?: boolean;
}) {
  const [mode, setMode] = React.useState<Mode>("rect");
  // Rect drawing state.
  const [box, setBox] = React.useState<RegionBox | null>(null);
  const [drag, setDrag] = React.useState<{ x: number; y: number } | null>(null);
  // Polygon drawing state: committed vertices + a live cursor for the rubber band.
  const [poly, setPoly] = React.useState<RegionPoint[]>([]);
  const [cursor, setCursor] = React.useState<RegionPoint | null>(null);
  // The drawn-but-not-yet-confirmed selection.
  const [pending, setPending] = React.useState<Pending | null>(null);
  const imgRef = React.useRef<HTMLImageElement>(null);
  const wrapRef = React.useRef<HTMLDivElement>(null);

  const busyAny = !!busy;

  const reset = React.useCallback(() => {
    setBox(null);
    setDrag(null);
    setPoly([]);
    setCursor(null);
    setPending(null);
  }, []);

  // Reset overlays when the image or mode changes.
  React.useEffect(() => {
    reset();
  }, [src, reset]);
  React.useEffect(() => {
    reset();
  }, [mode, reset]);

  // Normalize a client point into the IMAGE content box (object-contain → may be
  // letterboxed). Returns null when the click lands on the letterbox.
  const norm = (clientX: number, clientY: number): RegionPoint | null => {
    const r = imgRef.current?.getBoundingClientRect();
    if (!r || r.width === 0 || r.height === 0) return null;
    const rx = (clientX - r.left) / r.width;
    const ry = (clientY - r.top) / r.height;
    if (rx < 0 || rx > 1 || ry < 0 || ry > 1) return null;
    return { x: rx, y: ry };
  };

  // ── Rect handlers ─────────────────────────────────────────────────────────
  const onPointerDown = (e: React.MouseEvent) => {
    if (busyAny || mode !== "rect") return;
    const p = norm(e.clientX, e.clientY);
    if (!p) return;
    setPending(null);
    setDrag({ x: p.x, y: p.y });
    setBox({ x: p.x, y: p.y, w: 0, h: 0 });
  };
  const onPointerMoveRect = (p: RegionPoint) => {
    if (!drag) return;
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
    if (box && box.w > 0.01 && box.h > 0.01) setPending({ kind: "rect", box });
  };

  // ── Polygon handlers ──────────────────────────────────────────────────────
  const onClickPoly = (e: React.MouseEvent) => {
    if (busyAny || mode !== "poly") return;
    const p = norm(e.clientX, e.clientY);
    if (!p) return;
    // Close the polygon when clicking near the first vertex (≥3 pts).
    if (poly.length >= 3) {
      const d0 = Math.hypot(p.x - poly[0].x, p.y - poly[0].y);
      if (d0 < 0.025) {
        setPending({ kind: "poly", points: poly });
        setCursor(null);
        return;
      }
    }
    setPending(null);
    setPoly((prev) => [...prev, p]);
  };
  const undoPoint = () => {
    if (busyAny || mode !== "poly") return;
    setPending(null);
    setPoly((prev) => prev.slice(0, -1));
  };
  const finishPoly = () => {
    if (busyAny || mode !== "poly" || poly.length < 3) return;
    setPending({ kind: "poly", points: poly });
    setCursor(null);
  };

  // Shared move handler: rect drag + polygon rubber-band cursor.
  const onMove = (e: React.MouseEvent) => {
    const p = norm(e.clientX, e.clientY);
    if (mode === "rect") {
      if (p) onPointerMoveRect(p);
      return;
    }
    setCursor(p);
  };

  const confirmSelection = () => {
    if (!pending || busyAny) return;
    if (pending.kind === "rect") onRect(pending.box);
    else onPoly(pending.points);
  };

  // Map a normalized image-space point into wrapper-space % accounting for the
  // letterbox inset from object-contain.
  const toWrapPct = React.useCallback((p: RegionPoint): { x: number; y: number } | null => {
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
    return { x: (offX + p.x * sx) * 100, y: (offY + p.y * sy) * 100 };
  }, []);

  // Rect overlay box positioned in wrapper space.
  const overlayStyle = React.useMemo(() => {
    if (mode !== "rect" || !box || box.w <= 0 || box.h <= 0) return null;
    const tl = toWrapPct({ x: box.x, y: box.y });
    const br = toWrapPct({ x: box.x + box.w, y: box.y + box.h });
    if (!tl || !br) return null;
    return {
      left: `${tl.x}%`,
      top: `${tl.y}%`,
      width: `${br.x - tl.x}%`,
      height: `${br.y - tl.y}%`,
    };
  }, [mode, box, toWrapPct]);

  // Polygon points in wrapper-space % + an SVG points string (incl. live cursor).
  const polyWrap = React.useMemo(() => {
    if (mode !== "poly") return null;
    const wrap = wrapRef.current;
    if (!wrap) return null;
    const wr = wrap.getBoundingClientRect();
    if (wr.width === 0 || wr.height === 0) return null;
    const verts = poly.map((p) => toWrapPct(p)).filter(Boolean) as { x: number; y: number }[];
    if (!verts.length) return { verts: [] as { x: number; y: number }[], svgClosed: "", svgOpen: "", wpx: wr.width, hpx: wr.height };
    const live = !pending && cursor ? toWrapPct(cursor) : null;
    const px = (v: { x: number; y: number }) => `${(v.x / 100) * wr.width},${(v.y / 100) * wr.height}`;
    const open = verts.map(px).join(" ") + (live ? ` ${px(live)}` : "");
    const closed = verts.map(px).join(" ");
    return { verts, svgClosed: closed, svgOpen: open, wpx: wr.width, hpx: wr.height };
  }, [mode, poly, cursor, pending, toWrapPct]);

  const hint = busyAny
    ? "正在识别选中的区域…"
    : mode === "rect"
      ? "在图上拖拽框出要修改的区域，确认后再识别"
      : "依次点击勾出不规则范围，回到起点或点「闭合」完成";

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] text-fg-mute">{hint}</span>
        {/* Mode toggle: rectangle vs polygon lasso. */}
        <div className="inline-flex shrink-0 overflow-hidden rounded-md border border-line">
          <button
            type="button"
            onClick={() => setMode("rect")}
            disabled={busyAny}
            className={cn(
              "inline-flex items-center gap-1 px-2 py-1 text-[11px] transition-all duration-200 ease-out",
              mode === "rect" ? "bg-accent text-accent-fg" : "text-fg-mute hover:text-fg",
              busyAny && "cursor-not-allowed opacity-60",
            )}
          >
            <Square className="size-3" /> 矩形
          </button>
          <button
            type="button"
            onClick={() => setMode("poly")}
            disabled={busyAny}
            className={cn(
              "inline-flex items-center gap-1 px-2 py-1 text-[11px] transition-all duration-200 ease-out",
              mode === "poly" ? "bg-accent text-accent-fg" : "text-fg-mute hover:text-fg",
              busyAny && "cursor-not-allowed opacity-60",
            )}
          >
            <Lasso className="size-3" /> 任意形状
          </button>
        </div>
      </div>

      <div
        ref={wrapRef}
        className={cn(
          "relative w-full select-none overflow-hidden rounded-md bg-bg",
          busyAny ? "cursor-wait" : "cursor-crosshair",
          busyAny && "pointer-events-none",
        )}
        onMouseDown={onPointerDown}
        onMouseMove={onMove}
        onMouseUp={onPointerUp}
        onMouseLeave={() => {
          onPointerUp();
          if (mode === "poly") setCursor(null);
        }}
        onClick={onClickPoly}
      >
        <img
          ref={imgRef}
          src={src}
          alt="选区"
          draggable={false}
          className="max-h-[52vh] w-full select-none object-contain"
        />

        {/* ── Rect overlay: scrim + marching-ants border. ── */}
        {overlayStyle && (
          <>
            <div
              className="pointer-events-none absolute inset-0 transition-opacity duration-200 ease-out"
              style={{
                background: "rgba(9, 9, 11, 0.55)",
                clipPath: `polygon(
                  0% 0%, 100% 0%, 100% 100%, 0% 100%, 0% 0%,
                  ${overlayStyle.left} ${overlayStyle.top},
                  ${overlayStyle.left} calc(${overlayStyle.top} + ${overlayStyle.height}),
                  calc(${overlayStyle.left} + ${overlayStyle.width}) calc(${overlayStyle.top} + ${overlayStyle.height}),
                  calc(${overlayStyle.left} + ${overlayStyle.width}) ${overlayStyle.top},
                  ${overlayStyle.left} ${overlayStyle.top}
                )`,
              }}
            />
            <div
              className="pointer-events-none absolute animate-marching-ants"
              style={{
                ...overlayStyle,
                backgroundImage: [
                  "linear-gradient(90deg, hsl(var(--accent)) 50%, transparent 50%)",
                  "linear-gradient(90deg, hsl(var(--accent)) 50%, transparent 50%)",
                  "linear-gradient(0deg, hsl(var(--accent)) 50%, transparent 50%)",
                  "linear-gradient(0deg, hsl(var(--accent)) 50%, transparent 50%)",
                ].join(","),
                backgroundRepeat: "repeat-x, repeat-x, repeat-y, repeat-y",
                backgroundSize: "16px 2px, 16px 2px, 2px 16px, 2px 16px",
                backgroundPosition: "0 0, 0 100%, 0 0, 100% 0",
              }}
            />
          </>
        )}

        {/* ── Polygon overlay: scrim cutout + marching-ants outline + vertices. ── */}
        {polyWrap && polyWrap.verts.length > 0 && (
          <svg
            className="pointer-events-none absolute inset-0 h-full w-full"
            viewBox={`0 0 ${polyWrap.wpx} ${polyWrap.hpx}`}
            preserveAspectRatio="none"
          >
            {/* Dim outside the (closed) lasso once it has area. */}
            {polyWrap.verts.length >= 3 && (
              <path
                d={`M 0 0 H ${polyWrap.wpx} V ${polyWrap.hpx} H 0 Z M ${polyWrap.svgClosed
                  .split(" ")
                  .map((s, i) => (i === 0 ? `${s}` : `L ${s}`))
                  .join(" ")
                  .replace(/^/, "")} Z`}
                fill="rgba(9, 9, 11, 0.55)"
                fillRule="evenodd"
              />
            )}
            {/* The lasso outline: closed once staged/finished, else open with rubber band. */}
            <polyline
              points={pending ? polyWrap.svgClosed + " " + polyWrap.svgClosed.split(" ")[0] : polyWrap.svgOpen}
              fill="none"
              stroke="hsl(var(--accent))"
              strokeWidth={2}
              strokeDasharray="8 6"
              strokeLinejoin="round"
              strokeLinecap="round"
              className="animate-dash-flow"
            />
            {/* Vertex handles; first one larger as the close target. */}
            {polyWrap.verts.map((v, i) => (
              <circle
                key={i}
                cx={(v.x / 100) * polyWrap.wpx}
                cy={(v.y / 100) * polyWrap.hpx}
                r={i === 0 ? 5 : 3.5}
                fill={i === 0 ? "hsl(var(--accent))" : "hsl(var(--bg))"}
                stroke="hsl(var(--accent))"
                strokeWidth={1.5}
              />
            ))}
          </svg>
        )}

        {busyAny && (
          <div className="absolute inset-0 grid place-items-center bg-bg/40">
            <Loader2 className="size-5 animate-spin text-accent" />
          </div>
        )}
      </div>

      {/* Polygon in-progress controls (撤销 / 闭合) before a pending is staged. */}
      {mode === "poly" && !pending && poly.length > 0 && (
        <div className="flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={undoPoint}
            disabled={busyAny}
            className="inline-flex items-center gap-1 rounded-md border border-line px-2 py-1 text-[11px] text-fg-mute transition-all duration-200 ease-out hover:text-fg"
          >
            <Undo2 className="size-3" /> 撤销
          </button>
          <button
            type="button"
            onClick={finishPoly}
            disabled={busyAny || poly.length < 3}
            className={cn(
              "inline-flex items-center gap-1 rounded-md border border-accent/40 px-2 py-1 text-[11px] text-accent transition-all duration-200 ease-out",
              poly.length < 3 ? "cursor-not-allowed opacity-50" : "hover:bg-accent/10",
            )}
          >
            <Lasso className="size-3" /> 闭合范围
          </button>
        </div>
      )}

      {/* Confirm bar: a staged selection waits until the user explicitly triggers
          recognition, so selecting and 识别 are two distinct steps. */}
      {pending && (
        <div className="flex items-center justify-between gap-3 rounded-md border border-accent/30 bg-accent/5 px-3 py-2 transition-all duration-200 ease-out animate-fade-in">
          <span className="text-[12px] text-fg-mute">
            {busyAny ? "正在识别选中的区域…" : "已圈定该区域，确认后识别其特征"}
          </span>
          <button
            type="button"
            onClick={confirmSelection}
            disabled={busyAny}
            className={cn(
              "inline-flex shrink-0 items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-[12px] font-medium text-accent-fg transition-all duration-200 ease-out",
              busyAny ? "cursor-not-allowed opacity-60" : "hover:bg-accent/90",
            )}
          >
            {busyAny ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Sparkles className="size-3.5" />
            )}
            识别该区域
          </button>
        </div>
      )}
    </div>
  );
}
