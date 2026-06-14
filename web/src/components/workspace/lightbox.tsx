import * as React from "react";
import { Download, Crop, Scissors } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { MAX_SELECTED } from "@/store/controller";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import * as VisuallyHidden from "@radix-ui/react-visually-hidden";

// Lightbox previews an asset. Images get re-adjust + generate-video + icon
// inputs; video assets get a play surface plus an entry to in-browser 视频处理.
export function Lightbox({
  asset,
  onOpenChange,
  onCrop,
  onVideoOps,
}: {
  asset: Asset | null;
  onOpenChange: (open: boolean) => void;
  onCrop: (a: Asset) => void;
  onVideoOps: (a: Asset, op?: "trim" | "frame") => void;
}) {
  const app = useApp();
  const [adjust, setAdjust] = React.useState("");
  const [motion, setMotion] = React.useState("");

  React.useEffect(() => {
    setAdjust("");
    setMotion("");
  }, [asset]);

  if (!asset) return null;
  const isVideo = (asset.mime || "").startsWith("video/") || asset.kind === "video";

  const applyAdjust = () => {
    const txt = adjust.trim();
    if (!txt) return;
    const others = [...app.state.selected].filter((id) => id !== asset.id);
    onOpenChange(false);
    app.sendMessage(txt, others.length ? [asset.id, ...others].slice(0, MAX_SELECTED) : asset.id);
  };

  const genVideo = () => {
    const m = motion.trim();
    if (!m) {
      app.toast("先描述想要的动作，例如「让角色走起来」", "warn");
      return;
    }
    onOpenChange(false);
    app.sendMessage(`把这张图做成视频：${m}`, asset.id);
  };

  const download = () => {
    const a = document.createElement("a");
    a.href = `/api/session/${app.state.sessionId}/assets/${asset.id}/download`;
    a.click();
  };

  return (
    <Dialog open={!!asset} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(620px,94vw)]">
        <VisuallyHidden.Root>
          <DialogTitle>素材预览</DialogTitle>
        </VisuallyHidden.Root>
        <div className="space-y-3">
          {isVideo ? (
            <video src={asset.url} controls loop playsInline autoPlay className="max-h-[52vh] w-full rounded-md bg-bg object-contain" />
          ) : (
            <img src={asset.url} alt="预览" className="max-h-[52vh] w-full rounded-md bg-bg object-contain" />
          )}

          <div className="flex items-center gap-2">
            {!isVideo && (
              <Button variant="outline" size="sm" onClick={() => onCrop(asset)}>
                <Crop className="size-3.5" /> 切尺寸
              </Button>
            )}
            {isVideo && (
              <Button variant="outline" size="sm" disabled title="功能待完善" onClick={() => onVideoOps(asset)}>
                <Scissors className="size-3.5" /> 裁剪 / 抽帧（待完善）
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={download}>
              <Download className="size-3.5" /> 下载
            </Button>
          </div>

          {!isVideo && (
            <div className="space-y-3 border-t border-line pt-3">
              <div className="space-y-2">
                <input
                  value={adjust}
                  onChange={(e) => setAdjust(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && applyAdjust()}
                  placeholder="二次调整：描述你想怎么改这张图"
                  className="h-9 w-full rounded-md border border-line bg-bg-elev px-3 text-[13px] outline-none placeholder:text-fg-mute focus:border-accent/60"
                />
                <Button size="sm" className="w-full" onClick={applyAdjust}>应用调整</Button>
              </div>
              <div className="space-y-2 border-t border-line/60 pt-3">
                <input
                  value={motion}
                  onChange={(e) => setMotion(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && genVideo()}
                  placeholder="生成视频：描述动作，例如 让角色走起来 / 镜头缓慢推进"
                  className="h-9 w-full rounded-md border border-line bg-bg-elev px-3 text-[13px] outline-none placeholder:text-fg-mute focus:border-accent/60"
                />
                <Button size="sm" variant="subtle" className="w-full" onClick={genVideo}>根据这张图生成视频</Button>
              </div>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
