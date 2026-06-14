import { FFmpeg } from "@ffmpeg/ffmpeg";
import { fetchFile, toBlobURL } from "@ffmpeg/util";

// video-ffmpeg wraps ffmpeg.wasm for in-browser video crop (片段裁剪) and frame
// extraction (抽帧). The wasm core is served SAME-ORIGIN under /ffmpeg/* (copied
// into web/static at build time by the ffmpeg-core-assets Vite plugin, then
// embedded in the Go binary) — no external CDN, which was unreachable on the
// internal network. Lazy-loaded on first use; the FFmpeg instance is reused.
const CORE_BASE = "/ffmpeg";

let ffmpeg: FFmpeg | null = null;
let loadPromise: Promise<FFmpeg> | null = null;

// Conservative client-side limits — ffmpeg.wasm runs in a single browser thread
// and holds inputs in memory, so large/long videos are rejected up front.
export const MAX_VIDEO_BYTES = 60 * 1024 * 1024; // 60 MB
export const MAX_VIDEO_SECONDS = 60; // 60 s

export type FfmpegProgress = (ratio: number) => void;

async function getFFmpeg(onLog?: (msg: string) => void): Promise<FFmpeg> {
  if (ffmpeg) return ffmpeg;
  if (loadPromise) return loadPromise;
  loadPromise = (async () => {
    const inst = new FFmpeg();
    if (onLog) inst.on("log", ({ message }) => onLog(message));
    // toBlobURL fetches the same-origin core asset and wraps it in a blob URL so
    // the ffmpeg worker can importScripts it without cross-origin friction.
    await inst.load({
      coreURL: await toBlobURL(`${CORE_BASE}/ffmpeg-core.js`, "text/javascript"),
      wasmURL: await toBlobURL(`${CORE_BASE}/ffmpeg-core.wasm`, "application/wasm"),
    });
    ffmpeg = inst;
    return inst;
  })();
  return loadPromise;
}

function extOf(file: File): string {
  const fromName = file.name.split(".").pop()?.toLowerCase();
  if (fromName && fromName.length <= 5) return fromName;
  if (file.type.includes("webm")) return "webm";
  return "mp4";
}

// trimVideo cuts [start, end] (seconds) out of the source video via stream copy
// when possible, falling back is unnecessary for the common case. Returns an
// MP4 blob.
export async function trimVideo(
  file: File,
  startSec: number,
  endSec: number,
  onProgress?: FfmpegProgress,
): Promise<Blob> {
  if (endSec <= startSec) throw new Error("结束时间需大于开始时间");
  const dur = endSec - startSec;
  const ff = await getFFmpeg();
  const inName = `in.${extOf(file)}`;
  const outName = "out.mp4";

  const onProg = ({ progress }: { progress: number }) => onProgress?.(Math.min(1, Math.max(0, progress)));
  if (onProgress) ff.on("progress", onProg);
  try {
    await ff.writeFile(inName, await fetchFile(file));
    // Re-encode to guarantee an exact, seekable cut (copy can land on the wrong
    // keyframe). H.264 + AAC for broad compatibility.
    await ff.exec([
      "-ss", startSec.toFixed(3),
      "-i", inName,
      "-t", dur.toFixed(3),
      "-c:v", "libx264",
      "-preset", "veryfast",
      "-c:a", "aac",
      "-movflags", "+faststart",
      outName,
    ]);
    const data = await ff.readFile(outName);
    await safeDelete(ff, inName);
    await safeDelete(ff, outName);
    return new Blob([data as BlobPart], { type: "video/mp4" });
  } finally {
    if (onProgress) ff.off("progress", onProg);
  }
}

// extractFrame grabs a single still frame at `atSec` and returns a PNG blob.
export async function extractFrame(
  file: File,
  atSec: number,
  onProgress?: FfmpegProgress,
): Promise<Blob> {
  const ff = await getFFmpeg();
  const inName = `in.${extOf(file)}`;
  const outName = "frame.png";

  const onProg = ({ progress }: { progress: number }) => onProgress?.(Math.min(1, Math.max(0, progress)));
  if (onProgress) ff.on("progress", onProg);
  try {
    await ff.writeFile(inName, await fetchFile(file));
    await ff.exec([
      "-ss", atSec.toFixed(3),
      "-i", inName,
      "-frames:v", "1",
      outName,
    ]);
    const data = await ff.readFile(outName);
    await safeDelete(ff, inName);
    await safeDelete(ff, outName);
    return new Blob([data as BlobPart], { type: "image/png" });
  } finally {
    if (onProgress) ff.off("progress", onProg);
  }
}

async function safeDelete(ff: FFmpeg, name: string) {
  try {
    await ff.deleteFile(name);
  } catch {
    /* ignore cleanup errors */
  }
}

// probeDuration reads a video file's duration (seconds) via an off-DOM <video>
// element — far cheaper than spinning up ffmpeg just for metadata.
export function probeDuration(file: File): Promise<number> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const v = document.createElement("video");
    v.preload = "metadata";
    v.onloadedmetadata = () => {
      URL.revokeObjectURL(url);
      resolve(v.duration);
    };
    v.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("无法读取视频时长"));
    };
    v.src = url;
  });
}
