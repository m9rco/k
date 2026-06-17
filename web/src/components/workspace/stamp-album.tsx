import * as React from "react";
import { Download, Eye, RefreshCw } from "lucide-react";
import type { Asset, Channel, SizePreset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import * as api from "@/lib/api";
import { cn } from "@/lib/utils";

// StampAlbum — 集邮册视图：参考图 + 全渠道尺寸插槽，支持一键按渠道批量生成。
export function StampAlbum({ onPreview }: { onPreview: (a: Asset) => void }) {
  const app = useApp();
  const { state } = app;
  const [channels, setChannels] = React.useState<Channel[]>([]);
  const [group, setGroup] = React.useState("all");
  const [refId, setRefId] = React.useState<string | null>(null);
  // pendingSizeIds: sizeIds dispatched by "生成全部" not yet arrived as assets
  const [pending, setPending] = React.useState<Set<string>>(new Set());

  React.useEffect(() => {
    api.listPlatforms().then(setChannels).catch(() => setChannels([]));
  }, []);

  const assets = React.useMemo(() => [...state.assets.values()], [state.assets]);

  // Auto-select newest generated/upload asset as reference (only if current refId gone)
  React.useEffect(() => {
    setRefId((prev) => {
      if (prev && state.assets.has(prev)) return prev;
      const newest = assets
        .filter((a) => a.kind === "generated" || a.kind === "upload")
        .sort((a, b) => (b.createdAt ? Date.parse(b.createdAt) : 0) - (a.createdAt ? Date.parse(a.createdAt) : 0))[0];
      return newest?.id ?? null;
    });
  }, [assets]);

  // sizeId → newest filled asset
  const filledBySize = React.useMemo(() => {
    const map = new Map<string, Asset>();
    for (const a of assets) {
      if (!a.sizeId) continue;
      const ex = map.get(a.sizeId);
      if (!ex || Date.parse(a.createdAt ?? "0") > Date.parse(ex.createdAt ?? "0"))
        map.set(a.sizeId, a);
    }
    return map;
  }, [assets]);

  // Remove pending sizeIds that have now been filled
  React.useEffect(() => {
    setPending((prev) => {
      if (!prev.size) return prev;
      const next = new Set(prev);
      let changed = false;
      for (const sid of prev) { if (filledBySize.has(sid)) { next.delete(sid); changed = true; } }
      return changed ? next : prev;
    });
  }, [filledBySize]);

  const groups = React.useMemo(() => {
    const set = new Set<string>();
    for (const c of channels) if (c.group) set.add(c.group);
    return ["all", ...set];
  }, [channels]);

  const visible = React.useMemo(
    () => channels.filter((c) => group === "all" || c.group === group),
    [channels, group],
  );

  const refAsset = refId ? state.assets.get(refId) : undefined;
  const refCandidates = assets.filter((a) => a.kind === "generated" || a.kind === "upload");

  const generateChannel = (ch: Channel) => {
    if (!refId) return;
    const sizeIds = ch.assetTypes
      .flatMap((at) => at.sizes)
      .filter((s) => s.producible && !filledBySize.has(s.id) && !pending.has(s.id))
      .map((s) => s.id);
    if (!sizeIds.length) return;
    const labels = ch.assetTypes
      .flatMap((at) => at.sizes.filter((s) => sizeIds.includes(s.id)))
      .map((s) => `${ch.name} · ${s.name}`)
      .join("、");
    setPending((prev) => new Set([...prev, ...sizeIds]));
    app.sendMessage(
      `把这张图适配到以下平台尺寸，保留主体与核心宣发意图、不改变原图逻辑：${labels}`,
      refId,
      sizeIds,
    );
  };

  const regenerateSlot = (ch: Channel, sz: SizePreset) => {
    if (!refId) return;
    setPending((prev) => new Set([...prev, sz.id]));
    app.sendMessage(
      `把这张图适配到：${ch.name} · ${sz.name}，保留主体与核心宣发意图`,
      refId,
      [sz.id],
    );
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-4">
      {/* 参考图区 */}
      <div className="flex items-center gap-3 rounded-lg border border-line bg-bg-elev px-4 py-3">
        {refAsset ? (
          <>
            <img src={refAsset.url} alt="参考图" className="h-16 w-24 rounded-md object-cover" />
            <div className="flex flex-col gap-0.5">
              <span className="text-xs font-medium text-fg">参考图</span>
              {refAsset.width && <span className="text-[11px] tabular-nums text-fg-mute">{refAsset.width}×{refAsset.height}</span>}
            </div>
            {refCandidates.length > 1 && (
              <select
                className="ml-auto rounded-md border border-line bg-bg px-2 py-1 text-xs text-fg"
                value={refId ?? ""}
                onChange={(e) => setRefId(e.target.value)}
              >
                {refCandidates.map((a, i) => (
                  <option key={a.id} value={a.id}>图 {i + 1}</option>
                ))}
              </select>
            )}
          </>
        ) : (
          <p className="text-xs text-fg-mute">请先上传或生成一张参考图，再使用集邮册</p>
        )}
      </div>

      {/* group 过滤 tab */}
      {channels.length > 0 && (
        <Tabs value={group} onValueChange={setGroup}>
          <TabsList>
            {groups.map((g) => (
              <TabsTrigger key={g} value={g}>{g === "all" ? "全部" : g}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      )}

      {/* 集邮册渠道网格 */}
      <div className="flex-1 space-y-8 overflow-y-auto pb-6">
        {visible.map((ch) => {
          const allSizes = ch.assetTypes.flatMap((at) => at.sizes.filter((s) => s.producible));
          const canGenerate = allSizes.some((s) => !filledBySize.has(s.id) && !pending.has(s.id));
          return (
            <section key={ch.id}>
              <div className="mb-3 flex items-center gap-2">
                <span className="text-sm font-semibold tracking-tight text-fg">{ch.name}</span>
                <span className="text-[11px] text-fg-mute">{allSizes.length} 个尺寸</span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="ml-auto text-xs"
                  disabled={!refId || !canGenerate}
                  title={!refId ? "请先选择参考图" : !canGenerate ? "全部已生成" : undefined}
                  onClick={() => generateChannel(ch)}
                >
                  {canGenerate ? "生成全部 →" : "全部已生成"}
                </Button>
              </div>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(110px,1fr))] gap-2">
                {ch.assetTypes.flatMap((at) =>
                  at.sizes.map((sz) => (
                    <Slot
                      key={sz.id}
                      size={sz}
                      asset={filledBySize.get(sz.id)}
                      generating={pending.has(sz.id)}
                      onPreview={onPreview}
                      onRegenerate={() => regenerateSlot(ch, sz)}
                    />
                  ))
                )}
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}

function Slot({ size, asset, generating, onPreview, onRegenerate }: {
  size: SizePreset;
  asset?: Asset;
  generating: boolean;
  onPreview: (a: Asset) => void;
  onRegenerate: () => void;
}) {
  if (!size.producible) {
    return (
      <div className="flex aspect-square flex-col items-center justify-center gap-1 rounded-lg border border-line bg-bg-elev/40 px-2 opacity-40">
        <span className="text-center text-[10px] leading-tight text-fg-mute">{size.name}</span>
        <span className="text-[9px] tabular-nums text-fg-mute">{size.format ?? "—"}</span>
      </div>
    );
  }

  if (generating) {
    return (
      <div className="flex aspect-square flex-col items-center justify-center gap-1.5 rounded-lg border border-dashed border-accent/40 bg-accent/5">
        <div className="size-4 animate-spin rounded-full border-2 border-accent/30 border-t-accent" />
        <span className="text-[10px] text-accent/70">生成中</span>
      </div>
    );
  }

  if (asset) {
    return (
      <div className="group relative aspect-square overflow-hidden rounded-lg border border-line">
        <img src={asset.url} alt={size.name} className="h-full w-full object-cover" />
        <div className={cn(
          "absolute inset-0 flex items-end justify-center gap-1.5 bg-black/50 p-2",
          "opacity-0 transition-opacity duration-150 group-hover:opacity-100",
        )}>
          <button onClick={() => onPreview(asset)} className="grid size-7 place-items-center rounded-md bg-white/15 text-white backdrop-blur-sm hover:bg-white/25 transition-colors">
            <Eye className="size-3.5" />
          </button>
          <a href={asset.url} download className="grid size-7 place-items-center rounded-md bg-white/15 text-white backdrop-blur-sm hover:bg-white/25 transition-colors">
            <Download className="size-3.5" />
          </a>
          <button onClick={onRegenerate} className="grid size-7 place-items-center rounded-md bg-white/15 text-white backdrop-blur-sm hover:bg-white/25 transition-colors">
            <RefreshCw className="size-3.5" />
          </button>
        </div>
        <div className="absolute bottom-1 left-1 rounded bg-black/60 px-1 py-0.5 text-[9px] tabular-nums text-white/80">
          {size.width}×{size.height}
        </div>
      </div>
    );
  }

  // empty
  return (
    <div className="flex aspect-square flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-line bg-bg-elev/50 px-2">
      <span className="text-center text-[10px] leading-tight text-fg-dim">{size.name}</span>
      <span className="text-[9px] tabular-nums text-fg-mute">{size.width}×{size.height}</span>
    </div>
  );
}
