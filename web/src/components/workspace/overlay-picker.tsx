import * as React from "react";
import { Type } from "lucide-react";
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

// ANCHORS is the nine-grid placement vocabulary the overlay_text tool accepts,
// labelled in Chinese and laid out 3×3 so the picker mirrors the button's real
// position on the image. value is the literal anchor string the agent passes.
const ANCHORS = [
  { value: "top-left", label: "左上" },
  { value: "top", label: "上中" },
  { value: "top-right", label: "右上" },
  { value: "left", label: "左中" },
  { value: "center", label: "居中" },
  { value: "right", label: "右中" },
  { value: "bottom-left", label: "左下" },
  { value: "bottom", label: "下中" },
  { value: "bottom-right", label: "右下" },
] as const;

// STYLES are the three common look presets, each carrying the human-readable
// instruction phrasing the agent maps to overlay_text style fields (color /
// stroke / bg_color). Kept descriptive (not raw hex) so the chat bubble reads
// naturally while the agent still fills exact values.
const STYLES = [
  { id: "plain", label: "白字描边", hint: "白色文字 + 深色描边，叠在任意画面都清晰", phrase: "白色文字加深色描边" },
  { id: "cta", label: "CTA 按钮", hint: "强调色底板 + 白字，像一个可点的按钮", phrase: "做成强调色（紫色）圆角按钮底板、白色文字的 CTA 样式" },
  { id: "badge", label: "折扣角标", hint: "红色底板 + 白字，适合做促销角标", phrase: "做成红色底板、白色文字的折扣角标样式" },
] as const;

type StyleId = typeof STYLES[number]["id"];

// OverlayPicker is the quick-action dialog for deterministic text overlay. It
// collects a literal string, a nine-grid position and a look preset, then routes
// the request through the conversation agent (overlay_text) as a natural-language
// instruction carrying the source asset as ref — so the product (font-rendered,
// no garbled glyphs) lands back in the workspace like any other edit.
export function OverlayPicker({
  asset,
  onOpenChange,
}: {
  asset: Asset | null;
  onOpenChange: (open: boolean) => void;
}) {
  const app = useApp();
  const [text, setText] = React.useState("");
  const [anchor, setAnchor] = React.useState<string>("bottom");
  const [style, setStyle] = React.useState<StyleId>("plain");

  // Reset to defaults each time a new asset opens the dialog.
  React.useEffect(() => {
    if (asset) {
      setText("");
      setAnchor("bottom");
      setStyle("plain");
    }
  }, [asset]);

  const submit = () => {
    if (!asset) return;
    const value = text.trim();
    if (!value) {
      app.toast("请先输入要叠加的文字", "warn");
      return;
    }
    const pos = ANCHORS.find((a) => a.value === anchor)?.label ?? "下中";
    const stylePhrase = STYLES.find((s) => s.id === style)?.phrase ?? "";
    // Phrasing leads with a trigger word ("贴文字/加个CTA") so the agent routes to
    // overlay_text; anchor + style read as plain Chinese the tool maps to its
    // structured fields.
    const instruction = `在这张图的「${pos}」位置叠加文字「${value}」，${stylePhrase}，保证在安全区内、文字清晰不被裁切。`;
    app.sendMessage(instruction, asset.id);
    onOpenChange(false);
  };

  return (
    <Dialog open={!!asset} onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(460px,92vw)]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Type className="size-4 text-accent" /> 文字叠加
          </DialogTitle>
        </DialogHeader>

        <p className="-mt-1 mb-3 text-[11px] leading-relaxed text-fg-mute">
          服务端字体渲染，文字清晰无错字（不经过生图模型）。常用于 CTA 按钮、折扣角标、定档大字。
        </p>

        {/* 文字内容 */}
        <div className="mb-3">
          <label className="mb-1.5 block text-xs font-medium text-fg-dim">文字内容</label>
          <textarea
            autoFocus
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
            }}
            rows={2}
            placeholder="如：立即下载、限时5折、6月23日全平台上线"
            className="w-full resize-none rounded-md border border-line bg-bg px-2.5 py-2 text-[13px] leading-relaxed text-fg outline-none transition-all duration-200 ease-out focus:border-accent"
          />
        </div>

        {/* 位置：九宫格 */}
        <div className="mb-3">
          <label className="mb-1.5 block text-xs font-medium text-fg-dim">位置</label>
          <div className="grid w-[132px] grid-cols-3 gap-1">
            {ANCHORS.map((a) => (
              <button
                key={a.value}
                type="button"
                onClick={() => setAnchor(a.value)}
                className={cn(
                  "grid h-9 place-items-center rounded-md border text-[11px] transition-all duration-200 ease-out",
                  anchor === a.value
                    ? "border-accent bg-accent/15 text-accent"
                    : "border-line text-fg-mute hover:border-accent/50 hover:text-fg",
                )}
              >
                {a.label}
              </button>
            ))}
          </div>
        </div>

        {/* 样式预设 */}
        <div className="mb-1">
          <label className="mb-1.5 block text-xs font-medium text-fg-dim">样式</label>
          <div className="flex flex-wrap gap-1.5">
            {STYLES.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => setStyle(s.id)}
                title={s.hint}
                className={cn(
                  "rounded-md border px-2.5 py-1.5 text-xs transition-all duration-200 ease-out",
                  style === s.id
                    ? "border-accent bg-accent/15 text-accent"
                    : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
                )}
              >
                {s.label}
              </button>
            ))}
          </div>
          <p className="mt-1.5 text-[11px] leading-relaxed text-fg-mute">
            {STYLES.find((s) => s.id === style)?.hint}
          </p>
        </div>

        <div className="mt-4 flex items-center gap-3 border-t border-line pt-3">
          <span className="text-[11px] text-fg-mute">⌘/Ctrl + Enter 发送</span>
          <Button className="ml-auto" size="sm" disabled={!text.trim()} onClick={submit}>
            叠加文字
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
