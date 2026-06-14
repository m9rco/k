import { motion } from "framer-motion";
import { Upload, Wand2, Crop, Film, Globe, Search, RotateCcw, Trash2, X } from "lucide-react";
import type { Asset } from "@/lib/types";
import type { TimelineNode } from "@/lib/timeline";
import { relativeTime } from "@/lib/timeline";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { AssetCard } from "./asset-card";
import { usePerformedProgress, useElapsed } from "./use-performed-progress";
import { cn } from "@/lib/utils";

// Action phrase + icon per node kind, evoking a workshop production step.
const KIND_META: Record<TimelineNode["kind"], { label: string; Icon: typeof Wand2 }> = {
  upload: { label: "上传素材", Icon: Upload },
  generate: { label: "生成/编辑", Icon: Wand2 },
  crop: { label: "切尺寸", Icon: Crop },
  video: { label: "生成视频", Icon: Film },
  crawl: { label: "爬取素材", Icon: Globe },
  search: { label: "搜图", Icon: Search },
};

// title prefers the agent's understanding (task.note) when present, else the
// generic action phrase; crop/crawl/search with multiple products show a count.
function nodeTitle(node: TimelineNode): string {
  if (node.task?.note) return node.task.note;
  const base = KIND_META[node.kind].label;
  if ((node.kind === "crop" || node.kind === "crawl" || node.kind === "search") && node.assets.length > 1) {
    return `${base} ×${node.assets.length}`;
  }
  return base;
}

export function TimelineNodeRow({
  node,
  labels,
  nowMs,
  onPreview,
  onCrop,
  onVideo,
  onVideoOps,
}: {
  node: TimelineNode;
  labels: Map<string, string>;
  nowMs: number;
  onPreview: (a: Asset) => void;
  onCrop: (a: Asset) => void;
  onVideo: (a: Asset) => void;
  onVideoOps: (a: Asset, op?: "trim" | "frame") => void;
}) {
  const { Icon } = KIND_META[node.kind];
  const title = nodeTitle(node);
  const rel = node.assets[0]?.createdAt ? relativeTime(node.assets[0].createdAt, nowMs) : "";
  const dotClass = node.state === "running"
    ? "border-accent bg-accent/30 animate-pulse"
    : node.state === "failed"
      ? "border-danger bg-danger/30"
      : "border-line bg-bg-elev";

  return (
    <motion.li
      layout
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.2, ease: "easeOut" }}
      className="relative pl-7"
    >
      {/* Spine dot */}
      <span className={cn("absolute left-[7px] top-1.5 size-2.5 -translate-x-1/2 rounded-full border", dotClass)} />
      {/* Header: action phrase + relative time */}
      <div className="mb-1.5 flex items-center gap-1.5 text-xs">
        <Icon className="size-3.5 text-fg-dim" />
        <span className="font-medium text-fg" title={title}>{title}</span>
        {node.parentId && labels.get(node.parentId) && (
          <span className="text-fg-mute">· 由 {labels.get(node.parentId)} 加工</span>
        )}
        {rel && <span className="ml-auto shrink-0 text-fg-mute">{rel}</span>}
      </div>
      {/* Body */}
      <NodeBody node={node} labels={labels} onPreview={onPreview} onCrop={onCrop} onVideo={onVideo} onVideoOps={onVideoOps} />
    </motion.li>
  );
}

function NodeBody({
  node,
  labels,
  onPreview,
  onCrop,
  onVideo,
  onVideoOps,
}: {
  node: TimelineNode;
  labels: Map<string, string>;
  onPreview: (a: Asset) => void;
  onCrop: (a: Asset) => void;
  onVideo: (a: Asset) => void;
  onVideoOps: (a: Asset, op?: "trim" | "frame") => void;
}) {
  if (node.state === "done") {
    return (
      <div className="grid grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-2">
        {node.assets.map((a) => (
          <AssetCard
            key={a.id}
            asset={a}
            label={labels.get(a.id)}
            onPreview={onPreview}
            onCrop={onCrop}
            onVideo={onVideo}
            onVideoOps={onVideoOps}
          />
        ))}
      </div>
    );
  }
  // Running search batch: show the images downloaded so far alongside one
  // shimmer placeholder per still-pending slot (占位数 = 请求张数).
  if (node.kind === "search" && node.state === "running") {
    return (
      <SearchBatchBody node={node} labels={labels} onPreview={onPreview} onCrop={onCrop} onVideo={onVideo} onVideoOps={onVideoOps} />
    );
  }
  // Active or failed task node.
  return <ActiveNode node={node} />;
}

function SearchBatchBody({
  node,
  labels,
  onPreview,
  onCrop,
  onVideo,
  onVideoOps,
}: {
  node: TimelineNode;
  labels: Map<string, string>;
  onPreview: (a: Asset) => void;
  onCrop: (a: Asset) => void;
  onVideo: (a: Asset) => void;
  onVideoOps: (a: Asset, op?: "trim" | "frame") => void;
}) {
  const app = useApp();
  const arrived = node.assets.length;
  // count comes from the task_created announcement; fall back to what has
  // arrived so the grid never collapses if the count is unknown.
  const total = Math.max(node.task?.count ?? 0, arrived, 1);
  const pending = Math.max(0, total - arrived);
  return (
    <div className="space-y-1.5">
      <div className="grid grid-cols-[repeat(auto-fill,minmax(120px,1fr))] gap-2">
        {node.assets.map((a) => (
          <AssetCard
            key={a.id}
            asset={a}
            label={labels.get(a.id)}
            onPreview={onPreview}
            onCrop={onCrop}
            onVideo={onVideo}
            onVideoOps={onVideoOps}
          />
        ))}
        {Array.from({ length: pending }).map((_, i) => (
          <div key={`ph-${i}`} className="relative aspect-square overflow-hidden rounded-md border border-line bg-bg-elev-2">
            <div className="absolute inset-0 -translate-x-full animate-shimmer bg-gradient-to-r from-transparent via-fg/10 to-transparent" />
          </div>
        ))}
      </div>
      {node.task && (
        <button
          type="button"
          title="取消搜图"
          onClick={() => app.removeTask(node.task!.id)}
          className="flex items-center gap-1 text-[11px] text-fg-mute transition-colors hover:text-danger"
        >
          <X className="size-3" /> 取消（已找到 {arrived}/{total}）
        </button>
      )}
    </div>
  );
}

function ActiveNode({ node }: { node: TimelineNode }) {
  const app = useApp();
  const task = node.task!;
  const failed = node.state === "failed";
  const pct = usePerformedProgress(task);
  const elapsed = useElapsed(task);

  return (
    <div className="max-w-[260px] rounded-lg border border-line bg-bg-elev p-2.5">
      {!failed ? (
        <div className="space-y-1.5">
          <div className="relative h-20 overflow-hidden rounded-md bg-bg-elev-2">
            <div className="absolute inset-0 -translate-x-full animate-shimmer bg-gradient-to-r from-transparent via-fg/10 to-transparent" />
          </div>
          <div className="h-1 overflow-hidden rounded-full bg-bg">
            <div className="h-full rounded-full bg-accent transition-[width] duration-300 ease-linear" style={{ width: `${pct}%` }} />
          </div>
          <div className="flex items-center justify-between tabular-nums text-[11px] text-accent">
            <span>{pct}%</span>
            {elapsed && <span className="text-fg-mute">{elapsed}</span>}
          </div>
        </div>
      ) : (
        <div className="space-y-1.5 text-xs">
          <div className="font-medium text-danger">失败</div>
          {task.error && <div className="line-clamp-2 leading-relaxed text-fg-mute">{task.error}</div>}
          <div className="flex gap-1.5 pt-0.5">
            <Button variant="danger" size="xs" onClick={() => app.retryTask(task.id)}>
              <RotateCcw className="size-3" /> 重试
            </Button>
            <Button variant="outline" size="xs" onClick={() => app.removeTask(task.id)}>
              <Trash2 className="size-3" /> 移除
            </Button>
          </div>
        </div>
      )}
      {!failed && (
        <button
          type="button"
          title="取消任务"
          onClick={() => app.removeTask(task.id)}
          className="mt-1 flex items-center gap-1 text-[11px] text-fg-mute transition-colors hover:text-danger"
        >
          <X className="size-3" /> 取消
        </button>
      )}
    </div>
  );
}
