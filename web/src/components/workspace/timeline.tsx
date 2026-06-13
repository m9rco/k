import { AnimatePresence } from "framer-motion";
import type { Asset } from "@/lib/types";
import type { TimelineNode } from "@/lib/timeline";
import { TimelineNodeRow } from "./timeline-node";

// Timeline renders the workshop production line: a vertical spine with creation
// event nodes, newest first. Active task nodes sit at the top (active end).
export function Timeline({
  nodes,
  labels,
  nowMs,
  onPreview,
  onCrop,
  onVideo,
}: {
  nodes: TimelineNode[];
  labels: Map<string, string>;
  nowMs: number;
  onPreview: (a: Asset) => void;
  onCrop: (a: Asset) => void;
  onVideo: (a: Asset) => void;
}) {
  return (
    <div className="relative">
      {/* Spine */}
      <span className="absolute left-[7px] top-1 bottom-1 w-px bg-line" aria-hidden />
      <ul className="space-y-5">
        <AnimatePresence initial={false}>
          {nodes.map((node) => (
            <TimelineNodeRow
              key={node.key}
              node={node}
              labels={labels}
              nowMs={nowMs}
              onPreview={onPreview}
              onCrop={onCrop}
              onVideo={onVideo}
            />
          ))}
        </AnimatePresence>
      </ul>
    </div>
  );
}
