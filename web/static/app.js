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
  channels: [],
  streamingEl: null,
  reasoningOpen: null,
  pendingTools: [],
  prefs: new Map(),
  // 打字机渲染：前端按固定速率逐字吐出，独立于后端/代理的到达节奏。
  typeTarget: "",
  typeShown: 0,
  typeDone: false,
  typeTimer: null,
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
      if (done) {
        finishPendingTools();
        refreshContext();
      }
      break;
    }
    case "reasoning":
      appendReasoning((msg.data && msg.data.text) || "");
      break;
    case "tool_call":
      renderToolCall(msg.data || {});
      break;
    case "tool_result":
      applyToolResult(msg.data || {});
      break;
    case "error":
      toast((msg.data && msg.data.message) || "发生未知错误");
      break;
    default:
      break;
  }
}

// ---------- 对话渲染 ----------

// scrollChatToBottom keeps the log pinned to newest content, but only when the
// user is already near the bottom — so reading earlier messages mid-stream
// isn't yanked away (scroll anchoring).
function scrollChatToBottom(force) {
  const log = $("#chatLog");
  const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 120;
  if (force || nearBottom) log.scrollTop = log.scrollHeight;
}

function addBubble(role, text) {
  const log = $("#chatLog");
  const b = el("div", `msg msg-${role}`, text);
  log.appendChild(b);
  scrollChatToBottom(true); // a brand-new bubble always pins
  return b;
}

// appendAssistantDelta feeds the typewriter buffer rather than writing the DOM
// directly. Incremental frames append to the target; the done frame (which the
// backend sends as the *full* reply) becomes the authoritative target so we
// never lose or double text regardless of how chunks arrived. A timer reveals
// characters at a fixed rate, giving a steady per-character animation even when
// a proxy buffers the whole SSE response into one burst.
function appendAssistantDelta(text, done) {
  collapseReasoning(); // answer text ends & folds the current thinking block
  if (!state.streamingEl) {
    state.streamingEl = addBubble("assistant", "");
    state.typeTarget = "";
    state.typeShown = 0;
    state.typeDone = false;
  }
  if (done) {
    // The done frame carries the complete reply; treat it as ground truth but
    // only if it's at least as long as what we've accumulated (guards against
    // an empty/short terminal frame clobbering streamed content).
    if (text && text.length >= state.typeTarget.length) state.typeTarget = text;
    state.typeDone = true;
  } else if (text) {
    state.typeTarget += text;
  }
  pumpTyper();
}

// pumpTyper reveals buffered characters one tick at a time. It self-schedules
// until the shown text catches up to the target; on the done frame it finalizes
// the bubble and triggers a workspace refresh.
function pumpTyper() {
  if (state.typeTimer) return; // already pumping
  const step = () => {
    state.typeTimer = null;
    if (!state.streamingEl) return;
    if (state.typeShown < state.typeTarget.length) {
      // Reveal a few chars per tick; scale with backlog so a big burst still
      // animates quickly without feeling inst... and never blocks the UI.
      const backlog = state.typeTarget.length - state.typeShown;
      const take = Math.max(2, Math.floor(backlog / 24));
      state.typeShown = Math.min(state.typeTarget.length, state.typeShown + take);
      state.streamingEl.textContent = state.typeTarget.slice(0, state.typeShown);
      scrollChatToBottom();
      state.typeTimer = setTimeout(step, 16); // ~60fps cadence
      return;
    }
    // Caught up. If the stream is done, finalize; else wait for more input.
    if (state.typeDone) {
      state.streamingEl = null;
      state.typeTarget = "";
      state.typeShown = 0;
      state.typeDone = false;
      refreshWorkspace();
    }
  };
  step();
}

// flushTyper completes the in-progress assistant bubble immediately (reveals
// all buffered text and stops the timer). Used when the turn moves on to a
// thinking block or tool call so a half-typed bubble isn't left dangling.
function flushTyper() {
  if (state.typeTimer) {
    clearTimeout(state.typeTimer);
    state.typeTimer = null;
  }
  if (state.streamingEl && state.typeTarget) {
    state.streamingEl.textContent = state.typeTarget;
  }
  state.streamingEl = null;
  state.typeTarget = "";
  state.typeShown = 0;
  state.typeDone = false;
}

// appendReasoning renders the model's thinking as a dimmed, collapsible block,
// distinct from the final answer. Chunks accumulate into one block per thinking
// turn; the block streams expanded, then auto-folds once the answer or a tool
// call begins (see collapseReasoning).
function appendReasoning(text) {
  if (!text) return;
  flushTyper(); // a new thinking block starts a fresh answer later
  const log = $("#chatLog");
  if (!state.reasoningOpen) {
    const wrap = el("div", "msg reasoning");
    const head = el("button", "reasoning-head");
    head.type = "button";
    head.appendChild(el("span", "reasoning-tag", "思考中"));
    head.appendChild(el("span", "reasoning-chevron", "▾"));
    const body = el("div", "reasoning-body", "");
    head.onclick = () => wrap.classList.toggle("collapsed");
    wrap.appendChild(head);
    wrap.appendChild(body);
    log.appendChild(wrap);
    state.reasoningOpen = { wrap, head, body };
  }
  state.reasoningOpen.body.textContent += text;
  scrollChatToBottom();
}

// collapseReasoning folds the currently-open thinking block (if any) and marks
// it as finished, leaving an expand affordance. Idempotent.
function collapseReasoning() {
  const r = state.reasoningOpen;
  if (!r) return;
  r.wrap.classList.add("collapsed");
  const tag = r.head.querySelector(".reasoning-tag");
  if (tag) tag.textContent = "已思考";
  state.reasoningOpen = null;
}

function renderToolCall(data) {
  flushTyper(); // finalize any in-progress answer before the tool card
  collapseReasoning();
  const log = $("#chatLog");
  const card = el("div", "tool-card");
  if (data.id) card.dataset.callId = data.id;
  card.dataset.tool = data.name || "tool";
  const head = el("div", "tc-head");
  const spinner = el("span", "tc-spinner");
  head.appendChild(spinner);
  head.appendChild(el("span", "tc-name", data.name || "tool"));
  head.appendChild(el("span", "tc-state", "执行中"));
  card.appendChild(head);
  if (data.arguments) card.appendChild(el("div", "tc-args", data.arguments));
  log.appendChild(card);
  scrollChatToBottom();
  state.pendingTools.push(card);
}

// applyToolResult completes the matching tool card: stop spinner, mark
// success/error, append a short summary. Falls back to the oldest pending
// card when the backend cannot supply a precise call id.
function applyToolResult(data) {
  const card = matchPendingTool(data.name);
  if (!card) return;
  state.pendingTools = state.pendingTools.filter((c) => c !== card);
  const head = card.querySelector(".tc-head");
  const spinner = head && head.querySelector(".tc-spinner");
  const stateLabel = head && head.querySelector(".tc-state");
  const ok = data.status !== "error";
  if (spinner) {
    const mark = el("span", `tc-mark ${ok ? "ok" : "fail"}`, ok ? "✓" : "✗");
    spinner.replaceWith(mark);
  }
  if (stateLabel) stateLabel.textContent = ok ? "完成" : "失败";
  card.classList.add(ok ? "tc-done" : "tc-failed");
  const detail = ok ? data.summary : data.error;
  if (detail) card.appendChild(el("div", "tc-result", detail));
  scrollChatToBottom();
}

// matchPendingTool finds the spinning card for a tool name, else the oldest.
function matchPendingTool(name) {
  if (name) {
    const hit = state.pendingTools.find((c) => c.dataset.tool === name);
    if (hit) return hit;
  }
  return state.pendingTools[0] || null;
}

// finishPendingTools clears any still-spinning cards once the turn ends, so a
// dropped tool_result never leaves a card spinning forever.
function finishPendingTools() {
  for (const card of state.pendingTools) {
    const spinner = card.querySelector(".tc-spinner");
    if (spinner) spinner.replaceWith(el("span", "tc-mark ok", "✓"));
    card.classList.add("tc-done");
  }
  state.pendingTools = [];
}

// sendMessage sends a user message. ref, when supplied, is the explicit asset
// id the message acts on; it is passed per-call rather than read from shared
// state so an operation always targets the asset the user actually chose
// (avoids the "selected the 3rd, operated on the 1st" mismatch).
function sendMessage(text, ref) {
  const input = $("#msgInput");
  const value = (text != null ? text : input.value).trim();
  if (!value) return;
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
    toast("连接尚未就绪，请稍候");
    return;
  }
  addBubble("user", value);
  const payload = { type: "user_message", text: value };
  if (ref) payload.ref = ref;
  state.ws.send(JSON.stringify(payload));
  if (text == null) input.value = "";
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
  // Batch crop applies to the current multi-selection; hidden until something is selected.
  $("#batchCropBtn").hidden = state.selected.size === 0;
}

function taskCard(task) {
  const card = el("div", `card placeholder ${task.status === "failed" ? "failed" : ""}`);
  card.appendChild(el("div", "skeleton"));
  const status = el("div", "ph-status");
  const running = task.status === "running" || task.status === "queued";
  status.appendChild(el("div", null, running ? runningStage(task.progress) : statusLabel(task.status)));
  if (running) {
    const bar = el("div", "ph-bar");
    const fill = el("span");
    fill.style.width = (task.progress || 0) + "%";
    bar.appendChild(fill);
    status.appendChild(bar);
    status.appendChild(el("div", "ph-pct", (task.progress || 0) + "%"));
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

  // 左键切换多选；右键弹出操作菜单（放大/切尺寸/二次调整/下载）。
  card.onclick = () => toggleSelect(asset.id, card);
  card.oncontextmenu = (e) => {
    e.preventDefault();
    openAssetMenu(e.clientX, e.clientY, asset);
  };
  return card;
}

function statusLabel(s) {
  return { queued: "排队中", running: "生成中", failed: "失败", done: "完成" }[s] || s;
}

// runningStage maps progress to a human stage hint so a long generation reads
// as moving through phases rather than sitting on a static "生成中".
function runningStage(progress) {
  const p = progress || 0;
  if (p < 30) return "排队 · 准备中";
  if (p < 50) return "分析参考图";
  if (p < 80) return "生成中";
  return "收尾处理";
}

function toggleSelect(id, card) {
  if (state.selected.has(id)) {
    state.selected.delete(id);
    card.classList.remove("selected");
  } else {
    state.selected.add(id);
    card.classList.add("selected");
  }
  // Reflect selection-dependent actions (batch crop / zip) without a full re-render.
  $("#batchCropBtn").hidden = state.selected.size === 0;
}

// ---------- lightbox / 二次调整 ----------

function openLightbox(asset) {
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
    closeLightbox();
    sendMessage(txt, asset.id); // explicit target: this lightbox's asset
  };
}

function closeLightbox() {
  $("#lightbox").hidden = true;
}

// ---------- 资产右键菜单 ----------

// openAssetMenu positions the context menu at the cursor and wires its actions
// to the active asset. The menu is clamped to the viewport so it never spills
// off-screen near the right/bottom edges.
function openAssetMenu(x, y, asset) {
  const menu = $("#assetMenu");
  menu.hidden = false;
  const rect = menu.getBoundingClientRect();
  const left = Math.min(x, window.innerWidth - rect.width - 8);
  const top = Math.min(y, window.innerHeight - rect.height - 8);
  menu.style.left = left + "px";
  menu.style.top = top + "px";
  menu.onclick = (e) => {
    const act = e.target.dataset && e.target.dataset.act;
    if (!act) return;
    closeAssetMenu();
    runAssetAction(act, asset);
  };
}

function closeAssetMenu() {
  $("#assetMenu").hidden = true;
}

function runAssetAction(act, asset) {
  switch (act) {
    case "preview":
      openLightbox(asset);
      break;
    case "crop":
      openCapsule(asset.id);
      break;
    case "adjust":
      openLightbox(asset);
      $("#lbAdjustInput").focus();
      break;
    case "download":
      downloadSingle(asset.id);
      break;
  }
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

async function uploadOne(file) {
  const fd = new FormData();
  fd.append("file", file);
  const asset = await api(`/api/session/${state.sessionId}/upload`, { method: "POST", body: fd });
  state.assets.set(asset.id, asset);
  return asset;
}

// uploadFiles uploads one or many images concurrently, then renders once and
// shows a single summary toast (succeeded / failed counts).
async function uploadFiles(files) {
  const list = [...files].filter((f) => f && f.type.startsWith("image/"));
  if (!list.length) return;
  const results = await Promise.allSettled(list.map(uploadOne));
  renderWorkspace();
  const ok = results.filter((r) => r.status === "fulfilled").length;
  const fail = results.length - ok;
  if (fail === 0) {
    toast(ok === 1 ? "已上传，现在可以让我换背景/角色/文案" : `已上传 ${ok} 张图`, "ok");
  } else if (ok === 0) {
    toast(`上传失败 ${fail} 张`, "error");
  } else {
    toast(`已上传 ${ok} 张，失败 ${fail} 张`, "warn");
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
// assetIds holds one or many source assets the chosen sizes will be applied to.
const capsuleState = {
  assetIds: [],
  chosen: new Map(),
  groupFilter: "all",
  search: "",
  activeChannelId: null,
};

// openCapsule opens the size selector for one asset id or an array of ids
// (batch crop). All chosen sizes are applied to every source asset.
function openCapsule(assetIdOrIds) {
  capsuleState.assetIds = Array.isArray(assetIdOrIds) ? assetIdOrIds : [assetIdOrIds];
  capsuleState.chosen = new Map();
  capsuleState.groupFilter = "all";
  capsuleState.search = "";
  capsuleState.activeChannelId = state.channels.length ? state.channels[0].id : null;
  $("#channelSearch").value = "";
  $("#capsuleTitle").textContent =
    capsuleState.assetIds.length > 1
      ? `选择目标尺寸（${capsuleState.assetIds.length} 张）`
      : "选择目标尺寸";
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

  // Channel-level select-all for producible sizes.
  const producible = producibleSizes(ch);
  const bar = el("div", "size-panel-bar");
  const allChosen = producible.length > 0 && producible.every((sz) => capsuleState.chosen.has(sz.id));
  const selBtn = el("button", "ghost-btn small", allChosen ? "取消全选" : "全选可裁剪");
  selBtn.disabled = producible.length === 0;
  selBtn.onclick = () => {
    if (allChosen) {
      for (const sz of producible) capsuleState.chosen.delete(sz.id);
    } else {
      for (const sz of producible) {
        capsuleState.chosen.set(sz.id, { id: sz.id, label: `${ch.name} · ${sz.name}`, channel: ch.id });
      }
    }
    renderChannelList();
    renderSizePanel();
    updateChosenBar();
  };
  bar.appendChild(el("span", "size-panel-hint", `${ch.name} · 可裁剪 ${producible.length} 项`));
  bar.appendChild(selBtn);
  panel.appendChild(bar);

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

// producibleSizes flattens a channel's croppable (producible) sizes.
function producibleSizes(ch) {
  const out = [];
  for (const at of ch.assetTypes || []) {
    for (const sz of at.sizes || []) {
      if (sz.producible) out.push(sz);
    }
  }
  return out;
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
    const ids = items.map((i) => i.id);
    const labels = items.map((i) => i.label);
    for (const assetId of capsuleState.assetIds) {
      cropToSizes(assetId, ids, labels);
    }
    closeCapsule();
  };
}

function cropToSizes(assetId, sizeIds, labels) {
  if (!sizeIds.length) return;
  const shown = (labels && labels.length ? labels : sizeIds).join("、");
  sendMessage(`把这张图裁剪成这些尺寸：${shown}`, assetId); // explicit target
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

// ---------- 偏好关键词（前端本地提取） ----------

// 停用词：高频但无偏好信号的词/字，提取时过滤掉。含这些字的 2-gram 片段也丢弃，
// 避免出现「把背」「景换」这类跨词噪声。
const STOPWORDS = new Set([
  "把", "的", "了", "成", "这", "那", "张", "图", "一", "个", "和", "与", "帮",
  "我", "你", "请", "要", "想", "让", "给", "在", "对", "为", "是", "有", "做",
  "换", "改", "加", "下", "上", "里", "中", "再", "也", "就", "都", "还", "去",
  "的话", "一下", "一个", "这个", "那个", "可以", "需要", "怎么", "什么",
  "图片", "素材", "尺寸", "背景", "角色", "文案",
]);

// 单字停用字集合（从上面拆出），用于判断 2-gram 是否含噪声字。
const STOP_CHARS = new Set(
  [...STOPWORDS].filter((w) => w.length === 1)
);

// extractKeywords pulls candidate preference terms from a user message: CJK
// 2-4 char runs and ASCII words, minus stopwords. Long runs are sliced into
// 2-grams; any gram containing a stopword char is dropped to avoid cross-word
// noise. Lightweight, no backend.
function extractKeywords(text) {
  const out = [];
  for (const m of text.matchAll(/[一-龥]{2,}/g)) {
    const seg = m[0];
    if (seg.length <= 4) {
      if (!STOPWORDS.has(seg) && !hasStopChar(seg)) out.push(seg);
    } else {
      for (let i = 0; i < seg.length - 1; i++) {
        const pair = seg.slice(i, i + 2);
        if (!STOPWORDS.has(pair) && !hasStopChar(pair)) out.push(pair);
      }
    }
  }
  for (const m of text.matchAll(/[a-zA-Z][a-zA-Z0-9]{1,}/g)) {
    out.push(m[0].toLowerCase());
  }
  return out;
}

// hasStopChar reports whether a segment contains any single-char stopword.
function hasStopChar(seg) {
  for (const ch of seg) {
    if (STOP_CHARS.has(ch)) return true;
  }
  return false;
}

// recordPreferences accumulates keyword frequencies and refreshes the corner.
function recordPreferences(text) {
  for (const kw of extractKeywords(text)) {
    state.prefs.set(kw, (state.prefs.get(kw) || 0) + 1);
  }
  renderPrefs();
}

// renderPrefs shows the top keyword preferences; hides the corner when empty.
function renderPrefs() {
  const corner = $("#prefsCorner");
  const list = $("#prefsList");
  const top = [...state.prefs.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, 8)
    .filter(([, n]) => n >= 1);
  if (top.length === 0) {
    corner.hidden = true;
    return;
  }
  list.innerHTML = "";
  for (const [kw, n] of top) {
    const li = el("li", "pref-item");
    li.appendChild(el("span", "pref-word", kw));
    if (n > 1) li.appendChild(el("span", "pref-count", String(n)));
    list.appendChild(li);
  }
  corner.hidden = false;
}

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

// ---------- 未来能力路线图（仅展示规划） ----------

const ROADMAP = [
  { title: "自学习偏好", desc: "记住你的风格倾向，主动套用到新素材" },
  { title: "团队知识库", desc: "沉淀团队素材规范与品牌资产，生成时自动遵循" },
  { title: "生视频 / 动效", desc: "把静态宣发图一键转为短视频与动态素材" },
  { title: "物料爬取", desc: "按竞品/平台抓取参考物料，辅助构图与文案" },
];

function renderRoadmap() {
  const ul = $("#roadmapList");
  if (!ul) return;
  for (const item of ROADMAP) {
    const li = el("li", "roadmap-item");
    const head = el("div", "roadmap-head");
    head.appendChild(el("span", "roadmap-title", item.title));
    head.appendChild(el("span", "roadmap-badge", "规划中"));
    li.appendChild(head);
    li.appendChild(el("p", "roadmap-desc", item.desc));
    ul.appendChild(li);
  }
}

// ---------- 事件绑定 ----------

function bindUI() {
  $("#composer").addEventListener("submit", (e) => {
    e.preventDefault();
    const typed = $("#msgInput").value.trim();
    if (typed) recordPreferences(typed); // only genuine user-typed messages
    sendMessage();
  });

  const fileInput = $("#fileInput");
  $("#uploadBtn").onclick = () => fileInput.click();
  fileInput.onchange = () => {
    if (fileInput.files.length) uploadFiles(fileInput.files);
    fileInput.value = "";
  };

  $("#zipBtn").onclick = downloadZip;
  $("#selectAllBtn").onclick = () => {
    for (const id of state.assets.keys()) state.selected.add(id);
    renderWorkspace();
  };
  $("#batchCropBtn").onclick = () => {
    if (state.selected.size === 0) {
      toast("先选中要切尺寸的素材", "warn");
      return;
    }
    openCapsule([...state.selected]); // batch: apply chosen sizes to all selected
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

  // 右键菜单：点击外部 / Esc / 滚动 / 右键空白处时关闭
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#assetMenu")) closeAssetMenu();
  });
  document.addEventListener("contextmenu", (e) => {
    if (!e.target.closest(".card")) closeAssetMenu();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      closeAssetMenu();
      closeLightbox();
    }
  });
  $("#wsGrid").addEventListener("scroll", closeAssetMenu);

  // 拖拽上传到工作区
  const drop = document.querySelector(".workspace");
  ["dragover", "dragenter"].forEach((evt) =>
    drop.addEventListener(evt, (e) => e.preventDefault())
  );
  drop.addEventListener("drop", (e) => {
    e.preventDefault();
    if (e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
  });
}

// ---------- 启动 ----------

async function main() {
  bindUI();
  renderCapList();
  renderRoadmap();
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
