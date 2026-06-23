import type { Asset, Channel, ContextState, Task } from "./types";

// api wraps fetch with JSON handling and error propagation. Relative URLs hit
// the Go backend (same origin in prod, Vite proxy in dev).
async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, opts);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  const ct = res.headers.get("content-type") || "";
  return (ct.includes("application/json") ? res.json() : (res as unknown)) as T;
}

function fingerprint() {
  return {
    userAgent: navigator.userAgent,
    language: navigator.language,
    screen: `${screen.width}x${screen.height}`,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "",
    nonce: "",
  };
}

const SS_KEY = "gas.sessionId";

export async function bootSession(): Promise<string> {
  const existing = sessionStorage.getItem(SS_KEY) || "";
  const resp = await api<{ sessionId: string }>("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ fingerprint: fingerprint(), sessionId: existing }),
  });
  sessionStorage.setItem(SS_KEY, resp.sessionId);
  return resp.sessionId;
}

export function getContext(sid: string) {
  return api<{ estimatedTokens: number; budget: number; compressed: boolean; systemTokens?: number }>(
    `/api/session/${sid}/window`,
  ).then<ContextState>((w) => ({
    estimatedTokens: w.estimatedTokens,
    budget: w.budget,
    compressed: w.compressed,
    systemTokens: w.systemTokens,
  }));
}

export function clearContext(sid: string) {
  return api(`/api/session/${sid}/context/clear`, { method: "POST" });
}

export function listAssets(sid: string) {
  return api<{ assets: Asset[] }>(`/api/session/${sid}/assets`).then((r) => r.assets || []);
}

export function listTasks(sid: string) {
  return api<{ tasks: Task[] }>(`/api/session/${sid}/tasks`).then((r) => r.tasks || []);
}

export async function uploadFile(sid: string, file: File): Promise<Asset> {
  const fd = new FormData();
  fd.append("file", file);
  return api<Asset>(`/api/session/${sid}/upload`, { method: "POST", body: fd });
}

// uploadBlob wraps a generated Blob (e.g. a ffmpeg.wasm video clip or extracted
// frame) into a named File and uploads it through the same endpoint, so the
// product lands in the workspace alongside other assets.
export function uploadBlob(sid: string, blob: Blob, name: string): Promise<Asset> {
  return uploadFile(sid, new File([blob], name, { type: blob.type }));
}

export function deleteAsset(sid: string, assetId: string) {
  return api(`/api/session/${sid}/assets/${assetId}`, { method: "DELETE" });
}

export function clearWorkspace(sid: string) {
  return api(`/api/session/${sid}/clear`, { method: "POST" });
}

export function retryTask(sid: string, taskId: string) {
  return api(`/api/session/${sid}/tasks/${taskId}/retry`, { method: "POST" });
}

// retryAsset re-runs the AI flow that produced a SUCCEEDED product. Unlike
// retryTask (re-runs a failed task in place), this yields a NEW task whose
// product is a new asset; the original is preserved. Returns { status, taskId }.
export function retryAsset(sid: string, assetId: string): Promise<{ status: string; taskId: string }> {
  return api(`/api/session/${sid}/assets/${assetId}/retry`, { method: "POST" });
}

export function deleteTask(sid: string, taskId: string) {
  return api(`/api/session/${sid}/tasks/${taskId}`, { method: "DELETE" });
}

export function clearFailedTasks(sid: string) {
  return api(`/api/session/${sid}/tasks/failed/clear`, { method: "POST" });
}

export function listPlatforms() {
  return api<{ channels: Channel[] }>(`/api/platforms`).then((r) => r.channels || []);
}

// CropRect is a normalized crop region (each field ∈ [0,1]) for mode="rect".
export interface CropRect { x: number; y: number; w: number; h: number }

// CropOptions mirrors the backend crop strategy (cover default | contain |
// anchor | rect). Omitted fields fall back to cover on the server.
export interface CropOptions {
  mode?: "cover" | "contain" | "anchor" | "rect";
  anchor?: string;
  rect?: CropRect;
}

export function crop(sid: string, sourceAssetId: string, sizeIds: string[], lossless: boolean, opts?: CropOptions) {
  const body: Record<string, unknown> = { sourceAssetId, sizeIds, lossless };
  if (opts?.mode && opts.mode !== "cover") body.mode = opts.mode;
  if (opts?.mode === "anchor" && opts.anchor) body.anchor = opts.anchor;
  if (opts?.mode === "rect" && opts.rect) body.rect = opts.rect;
  return api<{ results: Asset[] }>(`/api/session/${sid}/crop`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

// LayerSplitResult is the outcome of a 图层精修 split: the locked canvas size
// (= source image size) plus the ordered layers (background first, then subjects).
// Each layer's box is its normalized position in the source frame — the
// background spans the whole frame {0,0,1,1}; each subject carries its verbatim
// crop box so the canvas places the cut-out sub-image back at its origin.
export interface SplitLayer {
  assetId: string;
  role: "background" | "subject";
  desc?: string;
  box: { x: number; y: number; w: number; h: number };
}
export interface LayerSplitResult {
  sourceAssetId: string;
  width: number;
  height: number;
  layers: SplitLayer[];
}

// layerSplit detects the source image's foreground subjects (people + marketing
// copy), crops each one VERBATIM out of the original pixels into its own layer,
// and uses the original image itself as the locked background — returning the
// layers for the fixed-size compositing canvas. Synchronous and deterministic
// (no generative model), so it returns in milliseconds.
export function layerSplit(sid: string, sourceAssetId: string): Promise<LayerSplitResult> {
  return api(`/api/session/${sid}/layer-split`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ sourceAssetId }),
  });
}

// persistComposite uploads a browser-flattened compositing-canvas PNG and lands
// it in the workspace as a "composite" asset. sourceAssetIds are the layers that
// were flattened (for timeline derivation labelling). The raw PNG bytes ARE the
// request body (streamed, not base64-bloated).
export function persistComposite(
  sid: string,
  blob: Blob,
  sourceAssetIds: string[],
): Promise<{ assetId: string; width: number; height: number; mime: string; bytes: number }> {
  const qs = sourceAssetIds.length ? `?sourceAssetIds=${encodeURIComponent(sourceAssetIds.join(","))}` : "";
  return api(`/api/session/${sid}/composite${qs}`, {
    method: "POST",
    headers: { "Content-Type": blob.type || "image/png" },
    body: blob,
  });
}

export function optimizePrompt(sid: string, text: string) {
  return api<{ optimized: string }>(`/api/session/${sid}/prompt/optimize`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  }).then((r) => r.optimized);
}

// RegionBox is a normalized selection rectangle (each field ∈ [0,1]).
export interface RegionBox { x: number; y: number; w: number; h: number }

// RegionPoint is a normalized vertex (each field ∈ [0,1]) for polygon (lasso)
// selection.
export interface RegionPoint { x: number; y: number }

// RegionResponse is the describe-region reply. `box` (when present) is the
// object's bounding box the vision model located for a click point — the
// frontend snaps the selection overlay to it.
export interface RegionResponse {
  available: boolean;
  description?: string;
  box?: RegionBox;
  error?: string;
}

// describeRegion resolves a selection on an asset into a structured feature
// description from the vision model. Three modes:
//   - point:   pass { px, py } (normalized click) — the model looks at the FULL
//              image, identifies the clicked object, returns its box + description.
//   - rect:    pass { x, y, w, h } — the model crops the box and describes it.
//   - polygon: pass { points: [{x,y},…] } — the server masks the lasso shape to
//              transparent outside, crops its bbox, and describes that cutout.
// Degrades gracefully: { available:false } means fall back to plain-text editing.
export function describeRegion(
  sid: string,
  assetId: string,
  sel: { px: number; py: number } | RegionBox | { points: RegionPoint[] },
) {
  return api<RegionResponse>(
    `/api/session/${sid}/assets/${assetId}/describe-region`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(sel),
    },
  );
}

// VisionReportResponse is the read-only marketing-analysis reply for an ordered
// reference group. `available:false` (or an error) means the frontend should
// simply hide the analysis block.
export interface VisionReportResponse {
  available: boolean;
  report?: string;
  count?: number;
  error?: string;
}

// visionReport fetches the marketing-analysis report for an ORDERED group of
// reference asset ids (≤16). Shares the adapt flow's cache, so a previously
// adapted group returns instantly. Degrades gracefully: { available:false }.
export function visionReport(sid: string, assetIds: string[], force = false) {
  return api<VisionReportResponse>(`/api/session/${sid}/vision-report`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ assetIds, force }),
  });
}

// saveVisionReport persists an edited marketing-analysis report under the same
// reference-group key, so the adapt flow and later views reuse the edit.
export function saveVisionReport(sid: string, assetIds: string[], report: string) {
  return api<{ status: string }>(`/api/session/${sid}/vision-report`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ assetIds, report }),
  });
}

// ModelEntry is defined in lib/types; re-exported here for callers of the API.
export type { ModelEntry } from "@/lib/types";

// ModelsResponse: catalog grouped by scene + the session's current selection +
// the server-preselected default model id per scene.
export interface ModelsResponse {
  catalog: Record<string, import("@/lib/types").ModelEntry[]>;
  selected: Record<string, string>;
  defaults: Record<string, string>;
}

export function getModels(sid: string) {
  return api<ModelsResponse>(`/api/session/${sid}/models`);
}

export function switchModel(sid: string, scene: string, model: string) {
  return api<{ status: string }>(`/api/session/${sid}/models`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ scene, model }),
  });
}

export function downloadSingleUrl(sid: string, assetId: string) {
  return `/api/session/${sid}/assets/${assetId}/download`;
}

export async function downloadZip(sid: string, assetIds: string[]) {
  const res = await fetch(`/api/session/${sid}/download/zip`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ assetIds }),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.blob();
}
