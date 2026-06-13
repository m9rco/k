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
  return api<{ estimatedTokens: number; budget: number; compressed: boolean }>(
    `/api/session/${sid}/window`,
  ).then<ContextState>((w) => ({
    estimatedTokens: w.estimatedTokens,
    budget: w.budget,
    compressed: w.compressed,
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

export function deleteAsset(sid: string, assetId: string) {
  return api(`/api/session/${sid}/assets/${assetId}`, { method: "DELETE" });
}

export function clearWorkspace(sid: string) {
  return api(`/api/session/${sid}/clear`, { method: "POST" });
}

export function retryTask(sid: string, taskId: string) {
  return api(`/api/session/${sid}/tasks/${taskId}/retry`, { method: "POST" });
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

export function crop(sid: string, sourceAssetId: string, sizeIds: string[], lossless: boolean) {
  return api<{ results: Asset[] }>(`/api/session/${sid}/crop`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ sourceAssetId, sizeIds, lossless }),
  });
}

export function optimizePrompt(sid: string, text: string) {
  return api<{ optimized: string }>(`/api/session/${sid}/prompt/optimize`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  }).then((r) => r.optimized);
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
