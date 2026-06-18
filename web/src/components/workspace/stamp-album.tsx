import * as React from "react";
import { Download, Eye, RefreshCw, AlertTriangle, Sparkles } from "lucide-react";
import type { Asset, Channel, SizePreset, Task } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import * as api from "@/lib/api";
import { cn } from "@/lib/utils";

const MAX_REFS = 16;

// Three ratio families for optimal reference coverage.
// Portrait:   < 0.80  (9:16 — 20 sizes)
// Landscape:  1.25–2.60 (16:9, ~2:1 — 63 sizes)
// Ultra-wide: > 2.60  (4:1–6:1 extreme banners — 13 sizes; hardest to adapt without a matching ref)
const RATIO_FAMILIES = [
  { id: "portrait",   label: "竖版", hint: "9:16",  aspectRatio: "9/16",  lo: 0,    hi: 0.80 },
  { id: "landscape",  label: "横版", hint: "16:9",  aspectRatio: "16/9",  lo: 1.25, hi: 2.60 },
  { id: "ultrawide",  label: "超宽", hint: "4:1+",  aspectRatio: "4/1",   lo: 2.60, hi: Infinity },
] as const;
type FamilyId = typeof RATIO_FAMILIES[number]["id"];

function computeCoverage(refIds: string[], assets: Map<string, Asset>): Set<FamilyId> {
  const covered = new Set<FamilyId>();
  for (const id of refIds) {
    const a = assets.get(id);
    if (!a?.width || !a?.height) continue;
    const r = a.width / a.height;
    for (const f of RATIO_FAMILIES) if (r >= f.lo && r < f.hi) covered.add(f.id);
  }
  return covered;
}

// StampAlbum — 集邮册视图：参考图行（上传图多选，最多16张）+ 全渠道尺寸插槽。
export function StampAlbum({ onPreview }: { onPreview: (a: Asset) => void }) {
  const app = useApp();
  const { state } = app;
  const [channels, setChannels] = React.useState<Channel[]>([]);
  const [group, setGroup] = React.useState("all");
  // refIds: ordered list of selected upload asset ids (sent to gpt-image-2).
  // Lazy-initialised from state.assets so remounts (panel switch) don't flash
  // an empty coverage state while the sync useEffect below catches up.
  const [refIds, setRefIds] = React.useState<string[]>(() =>
    [...state.assets.values()]
      .filter((a) => a.kind === "upload")
      .sort((a, b) => (a.createdAt ? Date.parse(a.createdAt) : 0) - (b.createdAt ? Date.parse(b.createdAt) : 0))
      .map((a) => a.id)
      .slice(0, MAX_REFS),
  );
  const [pending, setPending] = React.useState<Set<string>>(new Set());

  React.useEffect(() => {
    api.listPlatforms().then(setChannels).catch(() => setChannels([]));
  }, []);

  const assets = React.useMemo(() => [...state.assets.values()], [state.assets]);

  // uploads in creation order (the source pool for the reference row)
  const uploads = React.useMemo(
    () => assets
      .filter((a) => a.kind === "upload")
      .sort((a, b) => (a.createdAt ? Date.parse(a.createdAt) : 0) - (b.createdAt ? Date.parse(b.createdAt) : 0)),
    [assets],
  );

  // Auto-add new uploads to refIds (up to MAX_REFS); remove stale ids
  React.useEffect(() => {
    setRefIds((prev) => {
      const valid = prev.filter((id) => state.assets.has(id));
      const incoming = uploads
        .map((a) => a.id)
        .filter((id) => !valid.includes(id));
      const next = [...valid, ...incoming].slice(0, MAX_REFS);
      return next.length === prev.length && next.every((id, i) => id === prev[i]) ? prev : next;
    });
  }, [uploads, state.assets]);

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

  // sizeId → its adapt task, split by lifecycle. A task carries sizeId from the
  // task_queued event (Start/Retry); the album maps it back to the slot since it
  // submits over the agent WS and never sees the taskId. activeBySize keeps the
  // spinner alive when local `pending` was lost (page reload); failedBySize drives
  // the failure affordance. A non-terminal task wins over a terminal one for the
  // same size (a retry re-runs the SAME taskId, so collisions are transient).
  const { activeBySize, failedBySize } = React.useMemo(() => {
    const active = new Map<string, Task>();
    const failed = new Map<string, Task>();
    for (const t of state.tasks.values()) {
      if (!t.sizeId) continue;
      if (t.status === "running" || t.status === "queued") active.set(t.sizeId, t);
      else if (t.status === "failed") failed.set(t.sizeId, t);
    }
    // An active retry supersedes a stale failure for the same slot.
    for (const sid of active.keys()) failed.delete(sid);
    return { activeBySize: active, failedBySize: failed };
  }, [state.tasks]);

  // Clear local `pending` once a slot resolves either way (filled OR failed) so a
  // failed task stops the spinner and reveals the retry affordance instead of
  // hanging forever. (Originally only `filledBySize` cleared it — the gap that
  // left failed slots spinning with no recovery path.)
  React.useEffect(() => {
    setPending((prev) => {
      if (!prev.size) return prev;
      const next = new Set(prev);
      let changed = false;
      for (const sid of prev) {
        if (filledBySize.has(sid) || failedBySize.has(sid)) { next.delete(sid); changed = true; }
      }
      return changed ? next : prev;
    });
  }, [filledBySize, failedBySize]);

  const toggleRef = (id: string) => {
    setRefIds((prev) =>
      prev.includes(id)
        ? prev.filter((x) => x !== id)
        : prev.length < MAX_REFS ? [...prev, id] : prev,
    );
  };

  const groups = React.useMemo(() => {
    const set = new Set<string>();
    for (const c of channels) if (c.group) set.add(c.group);
    return ["all", ...set];
  }, [channels]);

  const visible = React.useMemo(
    () => channels.filter((c) => group === "all" || c.group === group),
    [channels, group],
  );

  const hasRefs = refIds.length > 0;
  const ref = hasRefs ? (refIds.length === 1 ? refIds[0] : refIds) : undefined;

  const coverage = React.useMemo(
    () => computeCoverage(refIds, state.assets),
    [refIds, state.assets],
  );
  const optimalCovered = coverage.size === RATIO_FAMILIES.length;

  const generateChannel = (ch: Channel) => {
    if (!hasRefs) return;
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
      `把${refIds.length > 1 ? `这 ${refIds.length} 张参考图` : "这张图"}适配到以下平台尺寸，保留主体与核心宣发意图、不改变原图逻辑：${labels}`,
      ref,
      sizeIds,
    );
  };

  const regenerateSlot = (ch: Channel, sz: SizePreset) => {
    if (!hasRefs) return;
    setPending((prev) => new Set([...prev, sz.id]));
    app.sendMessage(
      `把${refIds.length > 1 ? `这 ${refIds.length} 张参考图` : "这张图"}适配到：${ch.name} · ${sz.name}，保留主体与核心宣发意图`,
      ref,
      [sz.id],
    );
  };

  // Retry a failed slot in place. The backend re-runs the SAME taskId (reusing its
  // cached params + sizeId), so the slot re-binds automatically; we only need to
  // flip it back to the spinner. Falls back to a fresh adapt if the failed task is
  // gone (e.g. cleared), so the slot is never a dead end.
  const retrySlot = (ch: Channel, sz: SizePreset) => {
    const failed = failedBySize.get(sz.id);
    setPending((prev) => new Set([...prev, sz.id]));
    if (failed) app.retryTask(failed.id);
    else regenerateSlot(ch, sz);
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-4">
      {/* 参考图行：上传图一行排开，点击切换选中，最多16张 */}
      <div className="rounded-lg border border-line bg-bg-elev px-4 py-3">
        <div className="mb-2 flex items-center gap-2">
          <span className="text-xs font-medium text-fg">参考图</span>
          {uploads.length > 0 && (
            <span className="text-[11px] text-fg-mute">
              已选 {refIds.length}/{Math.min(uploads.length, MAX_REFS)}
            </span>
          )}
          {uploads.length > 0 && refIds.length > 0 && (
            optimalCovered
              ? <span className="ml-auto rounded-full bg-accent/15 px-2 py-0.5 text-[10px] font-medium text-accent">覆盖最佳 ✓</span>
              : <span className="ml-auto text-[10px] text-fg-mute">建议竖版 / 横版 / 超宽各一张</span>
          )}
        </div>
        {uploads.length === 0 ? (
          <p className="text-xs text-fg-mute">请先上传图片作为参考图，再使用集邮册</p>
        ) : (
          <>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {uploads.map((a) => {
              const selected = refIds.includes(a.id);
              const disabled = !selected && refIds.length >= MAX_REFS;
              return (
                <button
                  key={a.id}
                  type="button"
                  disabled={disabled}
                  onClick={() => toggleRef(a.id)}
                  className={cn(
                    "relative size-16 shrink-0 overflow-hidden rounded-md border-2 transition-all duration-150",
                    selected
                      ? "border-accent shadow-[0_0_0_2px] shadow-accent/30"
                      : "border-transparent opacity-60 hover:opacity-90",
                    disabled && "cursor-not-allowed opacity-30",
                  )}
                >
                  <img src={a.url} alt="" className="h-full w-full object-cover" />
                  {selected && (
                    <span className="absolute bottom-0.5 right-0.5 flex h-4 w-4 items-center justify-center rounded-full bg-accent text-[9px] font-bold text-white">
                      {refIds.indexOf(a.id) + 1}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
          {refIds.length > 0 && <RatioFamilyStamps coverage={coverage} />}
          </>
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
          const canGenerate = hasRefs && allSizes.some((s) => !filledBySize.has(s.id) && !pending.has(s.id));
          return (
            <section key={ch.id}>
              <div className="mb-3 flex items-center gap-2">
                <span className="text-sm font-semibold tracking-tight text-fg">{ch.name}</span>
                <span className="text-[11px] text-fg-mute">{allSizes.length} 个尺寸</span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="ml-auto text-xs"
                  disabled={!canGenerate}
                  title={!hasRefs ? "请先选择参考图" : undefined}
                  onClick={() => generateChannel(ch)}
                >
                  {hasRefs && !canGenerate ? "全部已生成" : "生成全部 →"}
                </Button>
              </div>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(110px,1fr))] gap-2">
                {ch.assetTypes.flatMap((at) =>
                  at.sizes.map((sz) => (
                    <Slot
                      key={sz.id}
                      size={sz}
                      asset={filledBySize.get(sz.id)}
                      generating={pending.has(sz.id) || activeBySize.has(sz.id)}
                      failed={failedBySize.has(sz.id)}
                      failReason={failedBySize.get(sz.id)?.error}
                      hasRefs={hasRefs}
                      onPreview={onPreview}
                      onRegenerate={() => regenerateSlot(ch, sz)}
                      onRetry={() => retrySlot(ch, sz)}
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

function Slot({ size, asset, generating, failed, failReason, hasRefs, onPreview, onRegenerate, onRetry }: {
  size: SizePreset;
  asset?: Asset;
  generating: boolean;
  failed: boolean;
  failReason?: string;
  hasRefs: boolean;
  onPreview: (a: Asset) => void;
  onRegenerate: () => void;
  onRetry: () => void;
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
        <div className="absolute inset-0 flex items-end justify-center gap-1.5 bg-black/50 p-2 opacity-0 transition-opacity duration-150 group-hover:opacity-100">
          <button onClick={() => onPreview(asset)} className="grid size-7 place-items-center rounded-md bg-white/15 text-white backdrop-blur-sm transition-colors hover:bg-white/25">
            <Eye className="size-3.5" />
          </button>
          <a href={asset.url} download className="grid size-7 place-items-center rounded-md bg-white/15 text-white backdrop-blur-sm transition-colors hover:bg-white/25">
            <Download className="size-3.5" />
          </a>
          <button onClick={onRegenerate} className="grid size-7 place-items-center rounded-md bg-white/15 text-white backdrop-blur-sm transition-colors hover:bg-white/25">
            <RefreshCw className="size-3.5" />
          </button>
        </div>
        <div className="absolute bottom-1 left-1 rounded bg-black/60 px-1 py-0.5 text-[9px] tabular-nums text-white/80">
          {size.width}×{size.height}
        </div>
      </div>
    );
  }
  if (failed) {
    return (
      <button
        type="button"
        onClick={onRetry}
        title={failReason ? `生成失败：${failReason}（点击重试）` : "生成失败，点击重试"}
        className="group flex aspect-square flex-col items-center justify-center gap-1.5 rounded-lg border border-danger/40 bg-danger/5 px-2 transition-all duration-200 ease-out hover:border-danger/60 hover:bg-danger/10"
      >
        <AlertTriangle className="size-4 text-danger/80" />
        <span className="text-[10px] text-danger/90">生成失败</span>
        <span className="flex items-center gap-1 text-[9px] text-fg-mute group-hover:text-fg-dim">
          <RefreshCw className="size-2.5" /> 重试
        </span>
      </button>
    );
  }
  return (
    <button
      type="button"
      onClick={onRegenerate}
      disabled={!hasRefs}
      title={hasRefs ? `生成：${size.name}` : "请先选择参考图"}
      className="group flex aspect-square flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-line bg-bg-elev/50 px-2 transition-all duration-200 ease-out hover:border-accent/50 hover:bg-accent/5 disabled:cursor-not-allowed disabled:hover:border-line disabled:hover:bg-bg-elev/50"
    >
      <Sparkles className="size-3.5 text-fg-mute transition-colors group-hover:text-accent group-disabled:text-fg-mute" />
      <span className="text-center text-[10px] leading-tight text-fg-dim">{size.name}</span>
      <span className="text-[9px] tabular-nums text-fg-mute">{size.width}×{size.height}</span>
    </button>
  );
}

// RatioFamilyStamps renders three aspect-ratio guide cards (portrait / square /
// landscape). Each card turns accent-coloured when the user's selected refs cover
// that family, giving at-a-glance feedback on reference quality.
function RatioFamilyStamps({ coverage }: { coverage: Set<FamilyId> }) {
  return (
    <div className="mt-2.5 flex gap-2">
      {RATIO_FAMILIES.map((f) => {
        const covered = coverage.has(f.id);
        return (
          <div
            key={f.id}
            style={{ aspectRatio: f.aspectRatio, height: 52 }}
            className={cn(
              "relative flex flex-col items-center justify-center rounded-md border px-2 transition-all duration-200",
              covered
                ? "border-accent bg-accent/8 text-accent"
                : "border-dashed border-line text-fg-mute",
            )}
          >
            <span className="text-[10px] font-medium leading-tight">{f.label}</span>
            <span className="text-[9px] tabular-nums opacity-60">{f.hint}</span>
            {covered && (
              <span className="absolute -right-1 -top-1 flex size-3.5 items-center justify-center rounded-full bg-accent text-[8px] font-bold text-white">✓</span>
            )}
          </div>
        );
      })}
    </div>
  );
}
