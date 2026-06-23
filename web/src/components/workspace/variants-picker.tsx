import * as React from "react";
import { Layers } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

// DIMENSIONS mirror generate_variants' four axes. label/hint are Chinese for the
// picker; phrase is the natural-language wording the agent maps back to the
// dimension key (style/palette/composition/copy).
const DIMENSIONS = [
  { id: "style", label: "风格", hint: "赛博 / 写实 / 动漫 / 水彩…不同画风", phrase: "不同风格" },
  { id: "palette", label: "配色", hint: "暖橙 / 冷蓝 / 撞色 / 莫兰迪…不同色调", phrase: "不同配色" },
  { id: "composition", label: "构图", hint: "居中 / 仰拍 / 特写 / 留白…不同构图", phrase: "不同构图" },
  { id: "copy", label: "文案侧重", hint: "卖点 / 紧迫感 / 口碑 / 福利…不同侧重", phrase: "不同文案侧重" },
] as const;

type DimId = typeof DIMENSIONS[number]["id"];

const COUNTS = [2, 3, 4, 5, 6, 7, 8] as const;

// VariantsPicker is the quick-action dialog for batch creative variants. It
// collects a variant axis, a count (2–8) and an optional shared direction, then
// routes the request through the conversation agent (generate_variants) carrying
// the source asset as ref. Each variant launches as its own async task; products
// stream into the workspace and group for side-by-side CTR comparison.
export function VariantsPicker({
  asset,
  onOpenChange,
}: {
  asset: Asset | null;
  onOpenChange: (open: boolean) => void;
}) {
  const app = useApp();
  const [dimension, setDimension] = React.useState<DimId>("style");
  const [count, setCount] = React.useState(4);
  const [brief, setBrief] = React.useState("");

  // Reset to defaults each time a new asset opens the dialog.
  React.useEffect(() => {
    if (asset) {
      setDimension("style");
      setCount(4);
      setBrief("");
    }
  }, [asset]);

  const submit = () => {
    if (!asset) return;
    const dim = DIMENSIONS.find((d) => d.id === dimension)!;
    const direction = brief.trim();
    // Phrasing leads with "批量变体" + count + axis so the agent routes to
    // generate_variants with the matching dimension/count; an optional shared
    // direction rides as a natural-language prefix the tool keeps for every variant.
    const instruction =
      `批量变体：把这张图一次出 ${count} 个${dim.phrase}的版本，分组对比测 CTR。` +
      (direction ? `统一方向：${direction}。` : "保留原图主体，只换场景/氛围。");
    app.sendMessage(instruction, asset.id);
    onOpenChange(false);
  };

  return (
    <Dialog open={!!asset} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(460px,92vw)]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Layers className="size-4 text-accent" /> 批量变体
          </DialogTitle>
        </DialogHeader>

        <p className="-mt-1 mb-3 text-[11px] leading-relaxed text-fg-mute">
          一次产出多个 creative 版本，保留原图主体、按所选维度重绘场景。每个变体独立并行，可分组对比。
        </p>

        {/* 维度 */}
        <div className="mb-3">
          <label className="mb-1.5 block text-xs font-medium text-fg-dim">变体维度</label>
          <div className="flex flex-wrap gap-1.5">
            {DIMENSIONS.map((d) => (
              <button
                key={d.id}
                type="button"
                onClick={() => setDimension(d.id)}
                className={cn(
                  "rounded-md border px-2.5 py-1.5 text-xs transition-all duration-200 ease-out",
                  dimension === d.id
                    ? "border-accent bg-accent/15 text-accent"
                    : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
                )}
              >
                {d.label}
              </button>
            ))}
          </div>
          <p className="mt-1.5 text-[11px] leading-relaxed text-fg-mute">
            {DIMENSIONS.find((d) => d.id === dimension)?.hint}
          </p>
        </div>

        {/* 数量 */}
        <div className="mb-3">
          <label className="mb-1.5 block text-xs font-medium text-fg-dim">数量</label>
          <div className="flex flex-wrap gap-1.5">
            {COUNTS.map((n) => (
              <button
                key={n}
                type="button"
                onClick={() => setCount(n)}
                className={cn(
                  "grid size-8 place-items-center rounded-md border text-xs tabular-nums transition-all duration-200 ease-out",
                  count === n
                    ? "border-accent bg-accent/15 text-accent"
                    : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
                )}
              >
                {n}
              </button>
            ))}
          </div>
        </div>

        {/* 统一方向（可选） */}
        <div className="mb-1">
          <label className="mb-1.5 block text-xs font-medium text-fg-dim">
            统一方向 <span className="font-normal text-fg-mute">（可选）</span>
          </label>
          <input
            value={brief}
            onChange={(e) => setBrief(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
            }}
            placeholder="如：赛博朋克夜景、春节喜庆氛围"
            className="w-full rounded-md border border-line bg-bg px-2.5 py-2 text-[13px] text-fg outline-none transition-all duration-200 ease-out focus:border-accent"
          />
        </div>

        <div className="mt-4 flex items-center gap-3 border-t border-line pt-3">
          <span className="text-[11px] text-fg-mute">将生成 {count} 个变体</span>
          <Button className="ml-auto" size="sm" onClick={submit}>
            生成变体
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
