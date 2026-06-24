import * as React from "react";
import { Check } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

// WorkspaceRefPicker — 从工作区挑选参考图的弹窗。列出所有静态图资产（排除视频），
// 多选后「加入」回填到图章参考行。selectedIds 是当前已选参考图（打开时预勾选），
// onConfirm 返回新的有序选择列表。
export function WorkspaceRefPicker({
  open,
  onOpenChange,
  selectedIds,
  maxRefs,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  selectedIds: string[];
  maxRefs: number;
  onConfirm: (ids: string[]) => void;
}) {
  const { state } = useApp();

  // Static-image assets only — a video frame is not a useful reference.
  const candidates = React.useMemo<Asset[]>(
    () =>
      [...state.assets.values()]
        .filter((a) => a.kind !== "video")
        .sort(
          (a, b) =>
            (a.createdAt ? Date.parse(a.createdAt) : 0) -
            (b.createdAt ? Date.parse(b.createdAt) : 0),
        ),
    [state.assets],
  );

  // Local ordered selection, re-seeded from the live selection each time the
  // dialog opens so reopening reflects any changes made meanwhile.
  const [picked, setPicked] = React.useState<string[]>(selectedIds);
  React.useEffect(() => {
    if (open) setPicked(selectedIds.filter((id) => state.assets.has(id)));
    // Only re-seed on open; selectedIds is intentionally read at that moment.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const toggle = (id: string) => {
    setPicked((prev) =>
      prev.includes(id)
        ? prev.filter((x) => x !== id)
        : prev.length < maxRefs
          ? [...prev, id]
          : prev,
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(760px,94vw)]">
        <DialogHeader>
          <DialogTitle>从工作区选择参考图</DialogTitle>
        </DialogHeader>

        <div className="flex items-center gap-2 text-xs text-fg-mute">
          <span>已选 {picked.length}/{maxRefs}</span>
        </div>

        {candidates.length === 0 ? (
          <p className="py-10 text-center text-sm text-fg-mute">工作区暂无可用图片</p>
        ) : (
          <div className="grid max-h-[55vh] grid-cols-[repeat(auto-fill,minmax(96px,1fr))] gap-2 overflow-y-auto pr-1">
            {candidates.map((a) => {
              const idx = picked.indexOf(a.id);
              const selected = idx >= 0;
              const disabled = !selected && picked.length >= maxRefs;
              return (
                <button
                  key={a.id}
                  type="button"
                  disabled={disabled}
                  onClick={() => toggle(a.id)}
                  title={disabled ? `最多选择 ${maxRefs} 张` : undefined}
                  className={cn(
                    "group relative aspect-square overflow-hidden rounded-md border-2 transition-all duration-200 ease-out",
                    selected
                      ? "border-accent shadow-[0_0_0_2px] shadow-accent/30"
                      : "border-transparent opacity-70 hover:opacity-100",
                    disabled && "cursor-not-allowed opacity-30 hover:opacity-30",
                  )}
                >
                  <img src={a.url} alt="" className="h-full w-full object-cover" />
                  {selected ? (
                    <span className="absolute bottom-1 right-1 flex size-4 items-center justify-center rounded-full bg-accent text-[9px] font-bold text-white">
                      {idx + 1}
                    </span>
                  ) : (
                    !disabled && (
                      <span className="absolute inset-0 flex items-center justify-center bg-black/0 transition-colors duration-200 group-hover:bg-black/20">
                        <Check className="size-5 text-white opacity-0 transition-opacity duration-200 group-hover:opacity-80" />
                      </span>
                    )
                  )}
                </button>
              );
            })}
          </div>
        )}

        <div className="mt-1 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button
            size="sm"
            onClick={() => {
              onConfirm(picked);
              onOpenChange(false);
            }}
          >
            加入
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
