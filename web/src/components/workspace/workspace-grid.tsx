import { AnimatePresence } from "framer-motion";
import type { Asset } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { AssetCard } from "./asset-card";
import { TaskCard } from "./task-card";
import { useApp } from "@/store/context";

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

// WorkspaceGrid is the "show everything" default view: three milestone sections
// (进行中 / 已完成 / 失败) as a responsive multi-column grid. No drag reorder;
// asset numbering (图N / 视频N) is supplied by labels (timeline/creation order).
export function WorkspaceGrid({
  assets,
  labels,
  onPreview,
  onCrop,
  onVideo,
  onLayerSplit,
}: {
  // assets in creation order (earliest first), matching the numbering labels.
  assets: Asset[];
  labels: Map<string, string>;
  onPreview: (a: Asset) => void;
  onCrop: (a: Asset) => void;
  onVideo: (a: Asset) => void;
  onLayerSplit: (a: Asset) => void;
}) {
  const app = useApp();
  const { state } = app;
  const active = [...state.tasks.values()].filter((t) => t.status === "queued" || t.status === "running");
  const failed = [...state.tasks.values()].filter((t) => t.status === "failed");
  // Newest-first for the completed grid (numbering stays creation-order via labels).
  const completed = [...assets].reverse();

  return (
    <>
      <Stage title="进行中" count={active.length}>
        <AnimatePresence initial={false}>
          {active.map((t) => <TaskCard key={t.id} task={t} />)}
        </AnimatePresence>
      </Stage>
      <Stage title="已完成" count={completed.length}>
        <AnimatePresence initial={false}>
          {completed.map((a) => (
            <AssetCard
              key={a.id}
              asset={a}
              label={labels.get(a.id)}
              onPreview={onPreview}
              onCrop={onCrop}
              onVideo={onVideo}
              onLayerSplit={onLayerSplit}
            />
          ))}
        </AnimatePresence>
      </Stage>
      <Stage
        title="失败"
        count={failed.length}
        action={<Button variant="ghost" size="xs" onClick={app.clearFailed}>清除全部</Button>}
      >
        <AnimatePresence initial={false}>
          {failed.map((t) => <TaskCard key={t.id} task={t} />)}
        </AnimatePresence>
      </Stage>
    </>
  );
}
