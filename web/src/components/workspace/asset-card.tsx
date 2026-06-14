import * as React from "react";
import { motion } from "framer-motion";
import { Search, Check, Play, SlidersHorizontal } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";

const KIND_LABEL: Record<string, string> = {
  upload: "上传", generated: "生成", cropped: "裁剪", crawled: "爬取", video: "视频",
};

export function AssetCard({
  asset,
  label,
  onPreview,
  onCrop,
  onVideo,
  onVideoOps,
}: {
  asset: Asset;
  label?: string;
  onPreview: (a: Asset) => void;
  onCrop: (a: Asset) => void;
  onVideo: (a: Asset) => void;
  onVideoOps: (a: Asset, op?: "trim" | "frame") => void;
}) {
  const app = useApp();
  const selected = app.state.selected.has(asset.id);
  const isVideo = (asset.mime || "").startsWith("video/") || asset.kind === "video";
  const vidRef = React.useRef<HTMLVideoElement>(null);
  const cardRef = React.useRef<HTMLDivElement>(null);

  const openMenu = (e: React.MouseEvent) => {
    e.stopPropagation();
    const el = cardRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    el.dispatchEvent(
      new MouseEvent("contextmenu", {
        bubbles: true,
        cancelable: true,
        clientX: rect.left + rect.width / 2,
        clientY: rect.top + rect.height / 2,
      }),
    );
  };

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <motion.div
          ref={cardRef}
          layout
          initial={{ opacity: 0, scale: 0.97 }}
          animate={{ opacity: 1, scale: 1 }}
          exit={{ opacity: 0, scale: 0.97 }}
          transition={{ duration: 0.2, ease: "easeOut" }}
          onClick={() => app.toggleSelect(asset.id)}
          onMouseEnter={() => isVideo && vidRef.current?.play().catch(() => {})}
          onMouseLeave={() => {
            if (isVideo && vidRef.current) {
              vidRef.current.pause();
              vidRef.current.currentTime = 0;
            }
          }}
          className={cn(
            "group relative aspect-square cursor-pointer overflow-hidden rounded-lg border bg-bg-elev",
            selected ? "border-accent ring-1 ring-accent" : "border-line",
          )}
        >
          {isVideo ? (
            <video ref={vidRef} src={asset.url} muted loop playsInline preload="metadata" className="h-full w-full bg-bg object-cover" />
          ) : (
            <img src={asset.url} alt={asset.kind} loading="lazy" className="h-full w-full object-cover" />
          )}

          {isVideo && (
            <span className="pointer-events-none absolute inset-0 grid place-items-center text-fg/80 transition-opacity group-hover:opacity-0">
              <span className="grid size-9 place-items-center rounded-full bg-black/45 backdrop-blur-sm">
                <Play className="size-4 translate-x-px" />
              </span>
            </span>
          )}

          <div className="absolute left-1.5 top-1.5 flex items-center gap-1">
            {label && (
              <span
                className={
                  isVideo
                    ? "rounded-md bg-accent-2/85 px-1.5 py-0.5 text-[10px] font-medium text-accent-2-fg backdrop-blur-sm"
                    : "rounded-md bg-accent/85 px-1.5 py-0.5 text-[10px] font-medium text-accent-fg backdrop-blur-sm"
                }
              >
                {label}
              </span>
            )}
            <span className="rounded-md bg-black/55 px-1.5 py-0.5 text-[10px] text-fg-dim backdrop-blur-sm">
              {isVideo ? "视频" : KIND_LABEL[asset.kind] || asset.kind}
            </span>
          </div>
          {!!(asset.width && asset.height) && (
            <span className="absolute bottom-1.5 left-1.5 rounded-md bg-black/55 px-1.5 py-0.5 text-[10px] tabular-nums text-fg-mute backdrop-blur-sm">
              {asset.width}×{asset.height}
            </span>
          )}

          <div className="absolute right-1.5 top-1.5 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100">
            <button
              type="button"
              title="放大查看"
              onClick={(e) => { e.stopPropagation(); onPreview(asset); }}
              className="grid size-6 place-items-center rounded-md bg-black/55 text-fg-dim backdrop-blur-sm transition-colors hover:text-fg"
            >
              <Search className="size-3.5" />
            </button>
            <button
              type="button"
              title="编辑操作"
              onClick={openMenu}
              className="grid size-6 place-items-center rounded-md bg-black/55 text-fg-dim backdrop-blur-sm transition-colors hover:text-fg"
            >
              <SlidersHorizontal className="size-3.5" />
            </button>
          </div>

          {selected && (
            <span className="absolute bottom-1.5 right-1.5 grid size-5 place-items-center rounded-full bg-accent text-accent-fg">
              <Check className="size-3" />
            </span>
          )}
        </motion.div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={() => onPreview(asset)}>放大预览</ContextMenuItem>
        {!isVideo && <ContextMenuItem onSelect={() => onCrop(asset)}>切尺寸</ContextMenuItem>}
        {!isVideo && <ContextMenuItem onSelect={() => onPreview(asset)}>二次调整</ContextMenuItem>}
        {!isVideo && <ContextMenuItem onSelect={() => onVideo(asset)}>生成视频</ContextMenuItem>}
        {isVideo && <ContextMenuItem onSelect={() => onVideoOps(asset, "trim")}>裁剪片段</ContextMenuItem>}
        {isVideo && <ContextMenuItem onSelect={() => onVideoOps(asset, "frame")}>抽帧</ContextMenuItem>}
        <ContextMenuItem onSelect={() => downloadAsset(app.state.sessionId, asset.id)}>下载</ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem destructive onSelect={() => app.removeAsset(asset.id)}>移除</ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function downloadAsset(sid: string, assetId: string) {
  const a = document.createElement("a");
  a.href = `/api/session/${sid}/assets/${assetId}/download`;
  a.download = "";
  document.body.appendChild(a);
  a.click();
  a.remove();
}
