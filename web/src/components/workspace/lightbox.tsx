import * as React from "react";
import { Download, Crop, Sparkles, Layers, X } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { MAX_SELECTED } from "@/store/controller";
import { describeRegion, type RegionBox, type RegionPoint } from "@/lib/api";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { RegionSelector } from "./region-selector";
import * as VisuallyHidden from "@radix-ui/react-visually-hidden";

// Lightbox previews an asset. Images get re-adjust + generate-video + icon
// inputs; video assets get a play surface.
export function Lightbox({
  asset,
  onOpenChange,
  onCrop,
  onLayerSplit,
}: {
  asset: Asset | null;
  onOpenChange: (open: boolean) => void;
  onCrop: (a: Asset) => void;
  onLayerSplit: (a: Asset) => void;
}) {
  const app = useApp();
  const [adjust, setAdjust] = React.useState("");
  const [motion, setMotion] = React.useState("");
  // Region-edit state: when selecting, the image is replaced by the selector;
  // regionDesc holds the structured feature description fetched for the drawn
  // region so the next edit can be scoped to that subject.
  const [selecting, setSelecting] = React.useState(false);
  const [describing, setDescribing] = React.useState(false);
  const [regionDesc, setRegionDesc] = React.useState("");

  React.useEffect(() => {
    setAdjust("");
    setMotion("");
    setSelecting(false);
    setDescribing(false);
    setRegionDesc("");
  }, [asset]);

  if (!asset) return null;
  const isVideo = (asset.mime || "").startsWith("video/") || asset.kind === "video";

  // describe runs the describe-region call for a drawn selection (rect or
  // polygon) and stages the structured description so the next edit can be
  // scoped to that subject. On failure it degrades to plain-text editing.
  const describe = async (sel: RegionBox | { points: RegionPoint[] }) => {
    setDescribing(true);
    try {
      const resp = await describeRegion(app.state.sessionId, asset.id, sel);
      if (resp.available && resp.description) {
        setRegionDesc(resp.description);
        app.toast("已识别该区域特征，可在下方补充修改要求", "ok");
      } else {
        app.toast("选区识别不可用，可直接用文字描述修改", "warn");
      }
    } catch {
      app.toast("选区识别失败，可直接用文字描述修改", "warn");
    } finally {
      setDescribing(false);
    }
  };

  const onRect = (box: RegionBox) => void describe(box);
  const onPoly = (points: RegionPoint[]) => void describe({ points });

  const applyAdjust = () => {
    const txt = adjust.trim();
    if (!txt && !regionDesc) return;
    const others = [...app.state.selected].filter((id) => id !== asset.id);
    const refs = others.length ? [asset.id, ...others].slice(0, MAX_SELECTED) : asset.id;
    onOpenChange(false);
    if (regionDesc) {
      // Compose a region-scoped instruction; the backend edit_image also receives
      // region_desc via the message path, but phrasing it here keeps the chat
      // transcript self-explanatory and works regardless of tool routing.
      const instruction = `只修改【选区主体：${regionDesc}】，其余画面保持不变。修改要求：${txt || "按描述优化该主体"}`;
      app.sendMessage(instruction, refs);
    } else {
      app.sendMessage(txt, refs);
    }
  };

  // extractLayer is superseded by 图层精修 (layer-split), driven from the parent
  // via onLayerSplit so the canvas runs the analyze→split flow for this image.

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
          ) : selecting ? (
            <RegionSelector
              src={asset.url}
              onRect={onRect}
              onPoly={onPoly}
              busy={describing}
            />
          ) : (
            <img src={asset.url} alt="预览" className="max-h-[52vh] w-full rounded-md bg-bg object-contain" />
          )}

          <div className="flex items-center gap-2">
            {!isVideo && (
              <Button variant="outline" size="sm" onClick={() => onCrop(asset)}>
                <Crop className="size-3.5" /> 适配尺寸
              </Button>
            )}
            {!isVideo && (
              <Button
                variant={selecting ? "subtle" : "outline"}
                size="sm"
                onClick={() => setSelecting((v) => !v)}
              >
                {selecting ? <X className="size-3.5" /> : <Sparkles className="size-3.5" />}
                {selecting ? "退出选区" : "圈定图层"}
              </Button>
            )}
            {!isVideo && (
              <Button variant="outline" size="sm" onClick={() => onLayerSplit(asset)}>
                <Layers className="size-3.5" /> 图层精修
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={download}>
              <Download className="size-3.5" /> 下载
            </Button>
          </div>

          {!isVideo && (
            <div className="space-y-3 border-t border-line pt-3">
              {regionDesc && (
                <div className="space-y-1 rounded-md border border-accent/30 bg-accent/5 p-2.5">
                  <div className="flex items-center justify-between">
                    <span className="text-[11px] font-medium text-accent">已锁定选区主体</span>
                    <button
                      type="button"
                      onClick={() => setRegionDesc("")}
                      className="text-fg-mute transition-colors hover:text-fg"
                    >
                      <X className="size-3.5" />
                    </button>
                  </div>
                  <textarea
                    value={regionDesc}
                    onChange={(e) => setRegionDesc(e.target.value)}
                    rows={3}
                    className="w-full resize-none rounded-md border border-line bg-bg-elev px-2.5 py-1.5 text-[12px] leading-relaxed outline-none focus:border-accent/60"
                  />
                </div>
              )}
              <div className="space-y-2">
                <input
                  value={adjust}
                  onChange={(e) => setAdjust(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && applyAdjust()}
                  placeholder={regionDesc ? "对这个选区主体想怎么改？" : "二次调整：描述你想怎么改这张图"}
                  className="h-9 w-full rounded-md border border-line bg-bg-elev px-3 text-[13px] outline-none placeholder:text-fg-mute focus:border-accent/60"
                />
                <Button size="sm" className="w-full" onClick={applyAdjust}>
                  {regionDesc ? "按选区应用调整" : "应用调整"}
                </Button>
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
