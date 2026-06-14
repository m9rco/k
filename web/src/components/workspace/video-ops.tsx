import * as React from "react";
import { Film, Image as ImageIcon } from "lucide-react";
import type { Asset } from "@/lib/types";
import { useApp } from "@/store/context";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import * as api from "@/lib/api";
import {
  trimVideo,
  extractFrame,
  probeDuration,
  MAX_VIDEO_BYTES,
  MAX_VIDEO_SECONDS,
} from "@/lib/video-ffmpeg";

type Op = "trim" | "frame";

// VideoOps runs in-browser video editing (片段裁剪 / 抽帧) via ffmpeg.wasm on a
// workspace video asset. The source bytes are fetched from the asset URL, the
// product is uploaded back through the upload API, and the workspace refreshes.
export function VideoOps({
  asset,
  initialOp = "trim",
  onOpenChange,
}: {
  asset: Asset | null;
  initialOp?: Op;
  onOpenChange: (open: boolean) => void;
}) {
  const app = useApp();
  const [op, setOp] = React.useState<Op>(initialOp);
  const [duration, setDuration] = React.useState<number | null>(null);
  const [start, setStart] = React.useState("0");
  const [end, setEnd] = React.useState("");
  const [at, setAt] = React.useState("0");
  const [busy, setBusy] = React.useState(false);
  const [progress, setProgress] = React.useState(0);
  const [stage, setStage] = React.useState("");
  const [err, setErr] = React.useState("");

  const fileRef = React.useRef<File | null>(null);

  React.useEffect(() => {
    setOp(initialOp);
    setDuration(null);
    setStart("0");
    setEnd("");
    setAt("0");
    setBusy(false);
    setProgress(0);
    setStage("");
    setErr("");
    fileRef.current = null;
    if (!asset) return;
    let cancelled = false;
    // Fetch the source video once so both probe + ffmpeg reuse the same bytes.
    (async () => {
      try {
        const res = await fetch(asset.url);
        const blob = await res.blob();
        const file = new File([blob], "source.mp4", { type: blob.type || "video/mp4" });
        if (cancelled) return;
        fileRef.current = file;
        if (file.size > MAX_VIDEO_BYTES) {
          setErr(`视频体积 ${(file.size / 1024 / 1024).toFixed(1)}MB 超过上限 ${MAX_VIDEO_BYTES / 1024 / 1024}MB`);
          return;
        }
        const d = await probeDuration(file);
        if (cancelled) return;
        setDuration(d);
        setEnd(Math.min(d, MAX_VIDEO_SECONDS).toFixed(1));
        if (d > MAX_VIDEO_SECONDS) {
          setErr(`视频时长 ${d.toFixed(1)}s 超过上限 ${MAX_VIDEO_SECONDS}s，仅可处理前 ${MAX_VIDEO_SECONDS}s`);
        }
      } catch (e) {
        if (!cancelled) setErr("加载源视频失败：" + (e as Error).message);
      }
    })();
    return () => { cancelled = true; };
  }, [asset, initialOp]);

  if (!asset) return null;

  const dur = duration ?? 0;
  const sNum = parseFloat(start) || 0;
  const eNum = parseFloat(end) || 0;
  const atNum = parseFloat(at) || 0;

  const validTrim =
    duration !== null && eNum > sNum && sNum >= 0 && eNum <= dur && (eNum - sNum) <= MAX_VIDEO_SECONDS;
  const validFrame = duration !== null && atNum >= 0 && atNum <= dur;

  const run = async () => {
    const file = fileRef.current;
    if (!file) {
      setErr("源视频尚未就绪，请稍候");
      return;
    }
    setBusy(true);
    setErr("");
    setProgress(0);
    try {
      if (op === "trim") {
        setStage("正在裁剪片段…");
        const out = await trimVideo(file, sNum, eNum, setProgress);
        setStage("上传产物…");
        await api.uploadBlob(app.state.sessionId, out, `clip-${Date.now()}.mp4`);
        app.toast(`已裁剪片段 ${sNum.toFixed(1)}–${eNum.toFixed(1)}s`, "ok");
      } else {
        setStage("正在抽帧…");
        const out = await extractFrame(file, atNum, setProgress);
        setStage("上传产物…");
        await api.uploadBlob(app.state.sessionId, out, `frame-${Date.now()}.png`);
        app.toast(`已抽取 ${atNum.toFixed(1)}s 处帧`, "ok");
      }
      await app.refreshWorkspace(app.state.sessionId);
      onOpenChange(false);
    } catch (e) {
      setErr((e as Error).message || "处理失败");
    } finally {
      setBusy(false);
      setStage("");
    }
  };

  const canRun = !busy && (op === "trim" ? validTrim : validFrame);

  return (
    <Dialog open={!!asset} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="w-[min(560px,94vw)]">
        <DialogHeader>
          <DialogTitle>视频处理</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <video src={asset.url} controls className="max-h-[40vh] w-full rounded-md bg-bg object-contain" />

          <div className="flex gap-1.5">
            <OpTab active={op === "trim"} onClick={() => setOp("trim")} icon={<Film className="size-3.5" />}>裁剪片段</OpTab>
            <OpTab active={op === "frame"} onClick={() => setOp("frame")} icon={<ImageIcon className="size-3.5" />}>抽帧</OpTab>
          </div>

          {duration !== null && (
            <p className="text-[11px] text-fg-mute">
              时长 {dur.toFixed(1)}s · 上限 {MAX_VIDEO_SECONDS}s / {MAX_VIDEO_BYTES / 1024 / 1024}MB
            </p>
          )}

          {op === "trim" ? (
            <div className="flex items-end gap-3">
              <Field label="开始 (秒)" value={start} onChange={setStart} disabled={busy} />
              <Field label="结束 (秒)" value={end} onChange={setEnd} disabled={busy} />
            </div>
          ) : (
            <Field label="时间点 (秒)" value={at} onChange={setAt} disabled={busy} />
          )}

          {busy && (
            <div className="space-y-1.5">
              <div className="h-1.5 overflow-hidden rounded-full bg-bg-elev-2">
                <div className="h-full bg-accent transition-all duration-200 ease-out" style={{ width: `${Math.round(progress * 100)}%` }} />
              </div>
              <p className="text-[11px] text-fg-dim">{stage} {progress > 0 && `${Math.round(progress * 100)}%`}</p>
            </div>
          )}

          {err && <p className="text-[11px] leading-relaxed text-danger">{err}</p>}

          <div className="flex items-center gap-3 border-t border-line pt-3">
            <span className="text-[11px] text-fg-mute">首次使用需加载处理内核，请稍候</span>
            <Button className="ml-auto" size="sm" disabled={!canRun} onClick={run}>
              {busy ? "处理中…" : op === "trim" ? "裁剪并保存" : "抽帧并保存"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function OpTab({ active, onClick, icon, children }: { active: boolean; onClick: () => void; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs transition-all duration-200 ease-out",
        active ? "border-accent bg-accent/15 text-accent" : "border-line text-fg-dim hover:border-accent/50 hover:text-fg",
      )}
    >
      {icon}{children}
    </button>
  );
}

function Field({ label, value, onChange, disabled }: { label: string; value: string; onChange: (v: string) => void; disabled?: boolean }) {
  return (
    <label className="flex-1 space-y-1">
      <span className="block text-[11px] text-fg-mute">{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        inputMode="decimal"
        className="h-9 w-full rounded-md border border-line bg-bg-elev px-3 text-[13px] tabular-nums outline-none placeholder:text-fg-mute focus:border-accent/60 disabled:opacity-50"
      />
    </label>
  );
}
