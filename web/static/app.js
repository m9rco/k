// Game Asset Studio — 前端单页应用（原生 ES 模块，无构建步骤）。
//
// 职责：
//  - 进入即基于浏览器指纹引导匿名 session（sessionStorage 复用）。
//  - WebSocket 承载对话：发送用户消息，渲染 Agent 增量回复与工具调用卡片。
//  - SSE 按 task id 订阅长任务进度，驱动工作区占位卡片状态。
//  - 工作区：列资产/任务、上传源图、点图二次调整、失败重试、下载/打包。
//  - toast 异常通知；偏好角落空时隐藏。

const SS_KEY = "gas.sessionId";

const state = {
  sessionId: null,
  ws: null,
  assets: new Map(),
  tasks: new Map(),
  taskStreams: new Map(),
  selected: new Set(),
  activeAssetId: null,
  channels: [],
  streamingEl: null,
};

const $ = (sel) => document.querySelector(sel);
const el = (tag, cls, text) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
};

async function api(path, opts = {}) {
  const res = await fetch(path, opts);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  const ct = res.headers.get("content-type") || "";
  return ct.includes("application/json") ? res.json() : res;
}

function toast(message, kind = "error") {
  const wrap = $("#toasts");
  const t = el("div", `toast ${kind}`, message);
  wrap.appendChild(t);
  setTimeout(() => {
    t.classList.add("hide");
    setTimeout(() => t.remove(), 260);
  }, 4200);
}

// ---------- 会话引导 ----------

function fingerprint() {
  return {
    userAgent: navigator.userAgent,
    language: navigator.language,
    screen: `${screen.width}x${screen.height}`,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "",
    nonce: "",
  };
}

async function bootSession() {
  const existing = sessionStorage.getItem(SS_KEY) || "";
  const resp = await api("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ fingerprint: fingerprint(), sessionId: existing }),
  });
  state.sessionId = resp.sessionId;
  sessionStorage.setItem(SS_KEY, resp.sessionId);
}

// ---------- 上下文面板 ----------

function setConn(ok) {
  const dot = $("#ctxDot");
  dot.classList.toggle("live", ok);
  dot.classList.toggle("off", !ok);
  $("#ctxText").textContent = ok ? "已连接" : "重连中…";
}

async function refreshContext() {
  if (!state.sessionId) return;
  try {
    const win = await api(`/api/session/${state.sessionId}/window`);
    const pct = win.budget ? Math.round((win.estimatedTokens / win.budget) * 100) : 0;
    $("#ctxBudget").textContent = `${pct}%${win.compressed ? " · 已压缩" : ""}`;
  } catch {
    /* 上下文是辅助信息，失败不打断主流程 */
  }
}

// ---------- WebSocket 对话 ----------

function connectWS() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/api/ws?session=${encodeURIComponent(state.sessionId)}`);
  ws.onopen = () => setConn(true);
  ws.onclose = () => {
    setConn(false);
    setTimeout(connectWS, 1500);
  };
  ws.onerror = () => ws.close();
  ws.onmessage = (ev) => {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }
    handleEvent(msg);
  };
  state.ws = ws;
}

function handleEvent(msg) {
  switch (msg.type) {
    case "message": {
      const { text, done } = msg.data || {};
      appendAssistantDelta(text || "", done);
      if (done) refreshContext();
      break;
    }
    case "tool_call":
      renderToolCall(msg.data || {});
      break;
    case "error":
      toast((msg.data && msg.data.message) || "发生未知错误");
      break;
    default:
      break;
  }
}

// ---------- 对话渲染 ----------

function addBubble(role, text) {
  const log = $("#chatLog");
  const b = el("div", `msg msg-${role}`, text);
  log.appendChild(b);
  log.scrollTop = log.scrollHeight;
  return b;
}

function appendAssistantDelta(text, done) {
  if (!state.streamingEl) {
    state.streamingEl = addBubble("assistant", "");
  }
  if (done) {
    if (text) state.streamingEl.textContent = text;
    state.streamingEl = null;
    refreshWorkspace();
  } else {
    state.streamingEl.textContent += text;
  }
  $("#chatLog").scrollTop = $("#chatLog").scrollHeight;
}

function renderToolCall(data) {
  const log = $("#chatLog");
  const card = el("div", "tool-card");
  const head = el("div", "tc-head");
  head.appendChild(el("span", "tc-spinner"));
  head.appendChild(el("span", null, data.name || "tool"));
  card.appendChild(head);
  if (data.arguments) card.appendChild(el("div", "tc-args", data.arguments));
  log.appendChild(card);
  log.scrollTop = log.scrollHeight;
}

function sendMessage(text) {
  const input = $("#msgInput");
  const value = (text != null ? text : input.value).trim();
  if (!value) return;
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
    toast("连接尚未就绪，请稍候");
    return;
  }
  addBubble("user", value);
  const payload = { type: "user_message", text: value };
  if (state.activeAssetId) payload.ref = state.activeAssetId;
  state.ws.send(JSON.stringify(payload));
  if (text == null) input.value = "";
  state.activeAssetId = null;
}

// ---------- 工作区 ----------

async function refreshWorkspace() {
  if (!state.sessionId) return;
  try {
    const [a, t] = await Promise.all([
      api(`/api/session/${state.sessionId}/assets`),
      api(`/api/session/${state.sessionId}/tasks`),
    ]);
    state.assets = new Map((a.assets || []).map((x) => [x.id, x]));
    state.tasks = new Map((t.tasks || []).map((x) => [x.id, x]));
    renderWorkspace();
    subscribeRunningTasks();
  } catch (e) {
    toast("工作区加载失败：" + e.message);
  }
}

function renderWorkspace() {
  const grid = $("#wsGrid");
  grid.innerHTML = "";

  let count = 0;
  for (const task of state.tasks.values()) {
    if (task.status === "done") continue;
    grid.appendChild(taskCard(task));
    count++;
  }
  for (const asset of state.assets.values()) {
    grid.appendChild(assetCard(asset));
    count++;
  }
  if (count === 0) {
    const empty = el("div", "ws-empty");
    empty.appendChild(el("div", "ws-empty-art"));
    empty.appendChild(el("p", null, "还没有素材。上传一张图或直接描述你的需求，产物会出现在这里。"));
    grid.appendChild(empty);
  }
  $("#zipBtn").hidden = state.assets.size === 0;
  $("#selectAllBtn").hidden = state.assets.size === 0;
}

function taskCard(task) {
  const card = el("div", `card placeholder ${task.status === "failed" ? "failed" : ""}`);
  card.appendChild(el("div", "skeleton"));
  const status = el("div", "ph-status");
  status.appendChild(el("div", null, statusLabel(task.status)));
  if (task.status === "running" || task.status === "queued") {
    const bar = el("div", "ph-bar");
    const fill = el("span");
    fill.style.width = (task.progress || 0) + "%";
    bar.appendChild(fill);
    status.appendChild(bar);
  }
  if (task.status === "failed") {
    if (task.error) status.appendChild(el("div", null, task.error));
    const retry = el("button", "retry-btn", "重试");
    retry.onclick = () => retryTask(task.id);
    status.appendChild(retry);
  }
  card.appendChild(status);
  return card;
}

function assetCard(asset) {
  const card = el("div", "card");
  if (state.selected.has(asset.id)) card.classList.add("selected");

  const img = el("img");
  img.loading = "lazy";
  img.src = asset.url;
  img.alt = asset.kind;
  card.appendChild(img);
  card.appendChild(el("div", "card-tag", asset.kind));

  const check = el("div", "card-check", "✓");
  card.appendChild(check);

  // 单击切换多选；双击放大并进入二次调整。
  card.onclick = () => toggleSelect(asset.id, card);
  card.ondblclick = (e) => {
    e.preventDefault();
    state.selected.delete(asset.id);
    card.classList.remove("selected");
    openLightbox(asset);
  };
  return card;
}

function statusLabel(s) {
  return { queued: "排队中", running: "生成中", failed: "失败", done: "完成" }[s] || s;
}

function toggleSelect(id, card) {
  if (state.selected.has(id)) {
    state.selected.delete(id);
    card.classList.remove("selected");
  } else {
    state.selected.add(id);
    card.classList.add("selected");
  }
}

// ---------- lightbox / 二次调整 ----------

function openLightbox(asset) {
  state.activeAssetId = asset.id;
  $("#lightboxImg").src = asset.url;
  $("#lbAdjustInput").value = "";
  $("#lightbox").hidden = false;
  $("#lbDownloadBtn").onclick = () => downloadSingle(asset.id);
  $("#lbCropBtn").onclick = () => {
    closeLightbox();
    openCapsule(asset.id);
  };
  $("#lbAdjustBtn").onclick = () => {
    const txt = $("#lbAdjustInput").value.trim();
    if (!txt) return;
    state.activeAssetId = asset.id;
    closeLightbox();
    sendMessage(txt);
  };
}

function closeLightbox() {
  $("#lightbox").hidden = true;
}

// ---------- SSE 任务进度 ----------

function subscribeRunningTasks() {
  for (const task of state.tasks.values()) {
    if (task.status === "done" || task.status === "failed") {
      closeStream(task.id);
      continue;
    }
    if (state.taskStreams.has(task.id)) continue;
    subscribeTask(task.id);
  }
}

function subscribeTask(taskId) {
  const es = new EventSource(`/api/tasks/${taskId}/events`);
  state.taskStreams.set(taskId, es);
  es.onmessage = (ev) => {
    let evt;
    try {
      evt = JSON.parse(ev.data);
    } catch {
      return;
    }
    applyTaskEvent(taskId, evt);
  };
  // EventSource 自动重连，浏览器带 Last-Event-ID 恢复最新状态。
}

function closeStream(taskId) {
  const es = state.taskStreams.get(taskId);
  if (es) {
    es.close();
    state.taskStreams.delete(taskId);
  }
}

function applyTaskEvent(taskId, evt) {
  const task = state.tasks.get(taskId) || { id: taskId, kind: "generate" };
  const d = evt.data || {};
  switch (evt.type) {
    case "task_queued":
      task.status = "queued";
      break;
    case "task_running":
    case "task_progress":
      task.status = "running";
      if (d.progress != null) task.progress = d.progress;
      break;
    case "task_done":
      task.status = "done";
      closeStream(taskId);
      refreshWorkspace();
      refreshContext();
      return;
    case "task_failed":
      task.status = "failed";
      task.error = d.error || "生成失败";
      closeStream(taskId);
      toast("有一个生成任务失败了，可在工作区重试", "warn");
      break;
  }
  state.tasks.set(taskId, task);
  renderWorkspace();
}

async function retryTask(taskId) {
  try {
    await api(`/api/session/${state.sessionId}/tasks/${taskId}/retry`, { method: "POST" });
    const task = state.tasks.get(taskId);
    if (task) {
      task.status = "queued";
      task.error = "";
      renderWorkspace();
    }
    subscribeTask(taskId);
  } catch (e) {
    toast("重试失败：" + e.message);
  }
}

// ---------- 上传 ----------

async function uploadFile(file) {
  const fd = new FormData();
  fd.append("file", file);
  try {
    const asset = await api(`/api/session/${state.sessionId}/upload`, { method: "POST", body: fd });
    state.assets.set(asset.id, asset);
    renderWorkspace();
    toast("已上传，现在可以让我换背景/角色/文案", "ok");
  } catch (e) {
    toast("上传失败：" + e.message);
  }
}

// ---------- 尺寸选择器（渠道 → 素材类型 → 尺寸） ----------

async function loadPlatforms() {
  try {
    const data = await api("/api/platforms");
    state.channels = data.channels || [];
  } catch {
    state.channels = [];
  }
}

// capsuleState holds the live selection while the sheet is open. chosen maps a
// size id → { id, label, channel } so the bottom bar can list cross-channel picks.
const capsuleState = {
  assetId: null,
  chosen: new Map(),
  groupFilter: "all",
  search: "",
  activeChannelId: null,
};

function openCapsule(assetId) {
  capsuleState.assetId = assetId;
  capsuleState.chosen = new Map();
  capsuleState.groupFilter = "all";
  capsuleState.search = "";
  capsuleState.activeChannelId = state.channels.length ? state.channels[0].id : null;
  $("#channelSearch").value = "";
  renderGroupTabs();
  renderChannelList();
  renderSizePanel();
  updateChosenBar();
  $("#capsuleSheet").hidden = false;
}

function closeCapsule() {
  $("#capsuleSheet").hidden = true;
}

// channelGroups returns the distinct coarse buckets (外渠/手机厂商/…) in order.
function channelGroups() {
  const seen = [];
  for (const ch of state.channels) {
    if (ch.group && !seen.includes(ch.group)) seen.push(ch.group);
  }
  return seen;
}

function renderGroupTabs() {
  const tabs = $("#channelGroupTabs");
  tabs.innerHTML = "";
  const groups = ["all", ...channelGroups()];
  for (const g of groups) {
    const btn = el("button", "group-tab", g === "all" ? "全部" : g);
    if (g === capsuleState.groupFilter) btn.classList.add("on");
    btn.onclick = () => {
      capsuleState.groupFilter = g;
      // Reset active channel to the first one visible under the new filter.
      const visible = visibleChannels();
      capsuleState.activeChannelId = visible.length ? visible[0].id : null;
      renderGroupTabs();
      renderChannelList();
      renderSizePanel();
    };
    tabs.appendChild(btn);
  }
}

// visibleChannels applies the current group filter and search query.
function visibleChannels() {
  const q = capsuleState.search.trim().toLowerCase();
  return state.channels.filter((ch) => {
    if (capsuleState.groupFilter !== "all" && ch.group !== capsuleState.groupFilter) return false;
    if (q && !ch.name.toLowerCase().includes(q) && !ch.id.toLowerCase().includes(q)) return false;
    return true;
  });
}

function renderChannelList() {
  const list = $("#channelList");
  list.innerHTML = "";
  const channels = visibleChannels();
  if (!channels.length) {
    list.appendChild(el("div", "channel-empty", "无匹配渠道"));
    return;
  }
  // Keep active channel valid within the visible set.
  if (!channels.some((c) => c.id === capsuleState.activeChannelId)) {
    capsuleState.activeChannelId = channels[0].id;
  }
  for (const ch of channels) {
    const item = el("button", "channel-item");
    item.appendChild(el("span", "channel-name", ch.name));
    const n = countChosenInChannel(ch.id);
    if (n > 0) item.appendChild(el("span", "channel-badge", String(n)));
    if (ch.id === capsuleState.activeChannelId) item.classList.add("on");
    item.onclick = () => {
      capsuleState.activeChannelId = ch.id;
      renderChannelList();
      renderSizePanel();
    };
    list.appendChild(item);
  }
}

function countChosenInChannel(channelId) {
  let n = 0;
  for (const v of capsuleState.chosen.values()) {
    if (v.channel === channelId) n++;
  }
  return n;
}

function renderSizePanel() {
  const panel = $("#sizePanel");
  panel.innerHTML = "";
  const ch = state.channels.find((c) => c.id === capsuleState.activeChannelId);
  if (!ch) {
    panel.appendChild(el("div", "channel-empty", "选择左侧渠道查看尺寸"));
    return;
  }
  for (const at of ch.assetTypes || []) {
    const group = el("div", "capsule-group");
    group.appendChild(el("h4", null, at.name || at.type));
    const row = el("div", "capsules");
    for (const sz of at.sizes || []) {
      row.appendChild(buildSizeChip(ch, sz));
    }
    group.appendChild(row);
    panel.appendChild(group);
  }
}

function buildSizeChip(ch, sz) {
  const chip = el("button", "capsule");
  chip.appendChild(document.createTextNode(sz.name));
  chip.appendChild(el("small", null, `${sz.width}×${sz.height}`));
  // Constraint hints as a tooltip + small note badge.
  const hints = [];
  if (sz.format) hints.push(sz.format.toUpperCase());
  if (sz.max_kb) hints.push(`≤${sz.max_kb}KB`);
  if (sz.note) hints.push(sz.note);
  if (hints.length) chip.title = hints.join(" · ");
  if (sz.note) chip.appendChild(el("em", "capsule-note", sz.note));

  if (!sz.producible) {
    chip.classList.add("disabled");
    chip.disabled = true;
    chip.title = (chip.title ? chip.title + " · " : "") + "该规格无法通过裁剪产出";
    return chip;
  }
  if (capsuleState.chosen.has(sz.id)) chip.classList.add("on");
  chip.onclick = () => {
    if (capsuleState.chosen.has(sz.id)) {
      capsuleState.chosen.delete(sz.id);
      chip.classList.remove("on");
    } else {
      capsuleState.chosen.set(sz.id, { id: sz.id, label: `${ch.name} · ${sz.name}`, channel: ch.id });
      chip.classList.add("on");
    }
    renderChannelList(); // refresh per-channel badges
    updateChosenBar();
  };
  return chip;
}

function updateChosenBar() {
  const n = capsuleState.chosen.size;
  $("#chosenCount").textContent = `已选 ${n} 项`;
  $("#capsuleConfirm").disabled = n === 0;
  $("#capsuleConfirm").onclick = () => {
    const items = [...capsuleState.chosen.values()];
    cropToSizes(capsuleState.assetId, items.map((i) => i.id), items.map((i) => i.label));
    closeCapsule();
  };
}

function cropToSizes(assetId, sizeIds, labels) {
  if (!sizeIds.length) return;
  state.activeAssetId = assetId;
  const shown = (labels && labels.length ? labels : sizeIds).join("、");
  sendMessage(`把这张图裁剪成这些尺寸：${shown}`);
}

// ---------- 下载 ----------

function downloadSingle(assetId) {
  triggerDownload(`/api/session/${state.sessionId}/assets/${assetId}/download`);
}

async function downloadZip() {
  if (state.selected.size === 0) {
    toast("先选中要打包的素材", "warn");
    return;
  }
  try {
    const res = await fetch(`/api/session/${state.sessionId}/download/zip`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ assetIds: [...state.selected] }),
    });
    if (!res.ok) throw new Error(await res.text());
    const skipped = res.headers.get("X-Skipped-Assets");
    if (skipped) toast(`已跳过无效条目：${skipped}`, "warn");
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    triggerDownload(url, "assets.zip");
    setTimeout(() => URL.revokeObjectURL(url), 5000);
    state.selected.clear();
    renderWorkspace();
  } catch (e) {
    toast("打包失败：" + e.message);
  }
}

function triggerDownload(url, filename) {
  const a = document.createElement("a");
  a.href = url;
  if (filename) a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// ---------- 引导能力清单 ----------

const SAMPLE_PROMPTS = [
  "把背景换成夜晚的赛博朋克城市",
  "把角色换成一个机甲战士，保留构图",
  "把文案改成「限时开服」",
  "裁成各平台尺寸 / 打包下载",
];

function renderCapList() {
  const ul = $("#capList");
  for (const p of SAMPLE_PROMPTS) {
    const li = el("li", null, p);
    li.onclick = () => {
      $("#msgInput").value = p;
      $("#msgInput").focus();
    };
    ul.appendChild(li);
  }
}

// ---------- 事件绑定 ----------

function bindUI() {
  $("#composer").addEventListener("submit", (e) => {
    e.preventDefault();
    sendMessage();
  });

  const fileInput = $("#fileInput");
  $("#uploadBtn").onclick = () => fileInput.click();
  fileInput.onchange = () => {
    if (fileInput.files[0]) uploadFile(fileInput.files[0]);
    fileInput.value = "";
  };

  $("#zipBtn").onclick = downloadZip;
  $("#selectAllBtn").onclick = () => {
    for (const id of state.assets.keys()) state.selected.add(id);
    renderWorkspace();
  };

  $("#capsuleClose").onclick = closeCapsule;
  $("#capsuleSheet").onclick = (e) => {
    if (e.target.id === "capsuleSheet") closeCapsule();
  };
  $("#channelSearch").oninput = (e) => {
    capsuleState.search = e.target.value;
    renderChannelList();
    renderSizePanel();
  };
  $("#lbClose").onclick = closeLightbox;
  $("#lightbox").onclick = (e) => {
    if (e.target.id === "lightbox") closeLightbox();
  };

  // 拖拽上传到工作区
  const drop = document.querySelector(".workspace");
  ["dragover", "dragenter"].forEach((evt) =>
    drop.addEventListener(evt, (e) => e.preventDefault())
  );
  drop.addEventListener("drop", (e) => {
    e.preventDefault();
    const f = e.dataTransfer.files[0];
    if (f && f.type.startsWith("image/")) uploadFile(f);
  });
}

// ---------- 启动 ----------

async function main() {
  bindUI();
  renderCapList();
  try {
    await bootSession();
  } catch (e) {
    toast("会话初始化失败：" + e.message);
    return;
  }
  await Promise.all([loadPlatforms(), refreshContext(), refreshWorkspace()]);
  connectWS();
  setInterval(refreshContext, 15000);
}

main();
