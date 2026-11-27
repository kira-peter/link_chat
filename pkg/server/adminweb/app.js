"use strict";
// link_chat admin console. Plain DOM, hash routes, no framework and no build
// step (design.md decision 10). Every request goes through api(), which adds
// the CSRF header and handles 401/403 in one place. All text from the server
// is inserted with textContent, never as HTML.

const $ = (sel, root = document) => root.querySelector(sel);

function el(tag, attrs = {}, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else if (v === true) n.setAttribute(k, "");
    else n.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    n.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return n;
}

// ---------- state ----------

const state = {
  me: null,        // {id, username, super, perms, all_perms}
  events: null,    // EventSource
  onEvent: null,   // handler of the current view
  disposeView: null,
};

const PERM_LABEL = {
  "stats.view": "查看统计",
  "chat.view": "查看聊天",
  "users.moderate": "处置用户",
  "accounts.manage": "管理账号",
};

const SECTIONS = [
  { id: "overview", label: "概览", perm: "stats.view", render: renderOverview },
  { id: "live", label: "实况", perm: "stats.view", render: renderLive },
  { id: "users", label: "用户", perm: "users.moderate", render: renderUsers },
  { id: "chats", label: "会话", perm: "chat.view", render: renderChats },
  { id: "search", label: "搜索", perm: "chat.view", render: renderSearch },
  { id: "moments", label: "动态", perm: "chat.view", render: renderMoments },
  { id: "accounts", label: "账号", perm: "accounts.manage", render: renderAccounts },
  { id: "audit", label: "审计", perm: "accounts.manage", render: renderAudit },
];

const can = (perm) => !!state.me && state.me.perms.includes(perm);

// ---------- api ----------

class ApiError extends Error {
  constructor(status, body) {
    super((body && body.message) || `HTTP ${status}`);
    this.status = status;
    this.code = body && body.code;
    this.retryAfterMs = (body && body.retry_after_ms) || 0;
  }
}

const ERR_TEXT = {
  invalid_credentials: "用户名或密码错误",
  forbidden: "没有权限",
  unauthorized: "需要登录",
  token_expired: "登录已过期",
  account_banned: "账号已被封禁",
  rate_limited: "尝试太频繁，请稍后再试",
  username_taken: "用户名已被占用",
  invalid_username: "用户名只能用字母、数字和 _ - .，3 到 32 个字符",
  weak_password: "密码至少 8 个字符",
  user_not_found: "用户不存在",
  conversation_not_found: "会话不存在",
  storage_unavailable: "服务端存储异常",
  moment_not_found: "动态不存在或已删除",
  not_found: "内容不存在或已删除",
};

// retryClock renders the moment a wait ends. The page cannot refresh itself, so
// a countdown would freeze; a fixed time stays true however long it is read.
function retryClock(ms) {
  const d = new Date(Date.now() + ms + 999);
  const t = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  if (d.toDateString() === new Date().toDateString()) return t;
  return `${d.getMonth() + 1}/${d.getDate()} ${t}`;
}

function errText(e) {
  if (e instanceof ApiError) {
    // Server messages for forbidden are specific (which rule refused it).
    if (e.code === "forbidden" && e.message && e.message !== "not permitted") return "没有权限：" + e.message;
    if (e.code === "rate_limited" && e.retryAfterMs > 0) return "尝试太频繁，请在 " + retryClock(e.retryAfterMs) + " 后重试";
    return ERR_TEXT[e.code] || e.message;
  }
  return "网络错误";
}

async function api(method, path, body) {
  const opts = { method, headers: {}, credentials: "same-origin" };
  if (method !== "GET") opts.headers["X-Requested-With"] = "link-chat-admin";
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch("api/" + path, opts);
  let data = null;
  try { data = await res.json(); } catch { /* empty body */ }
  if (res.status === 401) {
    // Signed out, expired, or banned: back to the login form.
    if (path !== "login") showLogin(errText(new ApiError(res.status, data)));
    throw new ApiError(res.status, data);
  }
  if (res.status === 403 && path === "me") {
    showLogin("这个账号已经没有任何管理权限");
    throw new ApiError(res.status, data);
  }
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

// ---------- formatting ----------

const pad = (n) => String(n).padStart(2, "0");
function fmtTime(ms) {
  if (!ms) return "";
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
function fmtShort(ms) {
  if (!ms) return "";
  const d = new Date(ms), now = new Date();
  if (d.toDateString() === now.toDateString()) return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return `${d.getMonth() + 1}/${d.getDate()}`;
}
function fmtUntil(ms) { return ms ? fmtTime(ms) : "永久"; }
function fmtDuration(sec) {
  const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  if (d) return `${d} 天 ${h} 小时`;
  if (h) return `${h} 小时 ${m} 分`;
  return `${m} 分`;
}
function fmtBytes(n) {
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`;
}

// ---------- toast and modal ----------

let toastTimer = 0;
function toast(msg, isErr = false) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.toggle("err", isErr);
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

// modal shows a form and resolves with submit(values) once it succeeds, or
// null when cancelled. submit may throw to keep the dialog open with an error.
function modal(title, body, okLabel, submit, danger = false) {
  return new Promise((resolve) => {
    const dlg = $("#modal"), form = $("#modal-form"), ok = $("#modal-ok");
    $("#modal-title").textContent = title;
    $("#modal-body").replaceChildren(...[body].flat());
    $("#modal-error").textContent = "";
    ok.textContent = okLabel;
    ok.className = danger ? "danger" : "";
    const onSubmit = async (ev) => {
      if (ev.submitter && ev.submitter.value === "cancel") return; // close normally
      ev.preventDefault();
      ok.disabled = true;
      try {
        const r = await submit(form);
        cleanup();
        dlg.close();
        resolve(r === undefined ? true : r);
      } catch (e) {
        $("#modal-error").textContent = e instanceof ApiError ? errText(e) : e.message;
      } finally {
        ok.disabled = false;
      }
    };
    const onClose = () => { cleanup(); resolve(null); };
    function cleanup() {
      form.removeEventListener("submit", onSubmit);
      dlg.removeEventListener("close", onClose);
    }
    form.addEventListener("submit", onSubmit);
    dlg.addEventListener("close", onClose, { once: true });
    dlg.showModal();
    const first = $("#modal-body input, #modal-body select, #modal-body textarea");
    if (first) first.focus();
  });
}

// ---------- sign in and shell ----------

function showLogin(msg = "") {
  closeEvents();
  clearView();
  state.me = null;
  $("#shell").hidden = true;
  $("#login").hidden = false;
  $("#login-error").textContent = msg;
  $("#login-form").password.value = "";
  $("#login-form").username.focus();
}

$("#login-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const f = ev.target;
  $("#login-error").textContent = "";
  const btn = f.querySelector("button");
  btn.disabled = true;
  try {
    state.me = await api("POST", "login", { username: f.username.value.trim(), password: f.password.value });
    f.password.value = "";
    enterShell();
  } catch (e) {
    $("#login-error").textContent = errText(e);
  } finally {
    btn.disabled = false;
  }
});

$("#logout").addEventListener("click", async () => {
  try { await api("POST", "logout"); } catch { /* already gone */ }
  showLogin();
});

function enterShell() {
  $("#login").hidden = true;
  $("#shell").hidden = false;
  $("#me").textContent = state.me.username + (state.me.super ? "（超级管理员）" : "");
  const nav = $("#nav");
  nav.replaceChildren(...SECTIONS.filter((s) => can(s.perm)).map((s) => el("a", { href: "#" + s.id, "data-id": s.id }, s.label)));
  openEvents();
  route();
}

function route() {
  if (!state.me) return;
  const allowed = SECTIONS.filter((s) => can(s.perm));
  const [id, ...rest] = location.hash.slice(1).split("/");
  let sec = allowed.find((s) => s.id === id);
  if (!sec) {
    sec = allowed[0];
    history.replaceState(null, "", "#" + sec.id);
  }
  for (const a of $("#nav").children) a.classList.toggle("on", a.dataset.id === sec.id);
  const view = $("#view");
  clearView();
  view.className = "view";
  view.replaceChildren();
  sec.render(view, rest.map(decodeURIComponent));
}
function clearView() {
  state.disposeView?.();
  state.disposeView = null;
  state.onEvent = null;
}
window.addEventListener("hashchange", route);

async function renderLive(view) {
  const placeholder = el("p", { role: "status" }, "正在加载实况…");
  view.append(placeholder);
  let dispose;
  let closed = false;
  state.disposeView = () => { closed = true; dispose?.(); };
  try {
    const { mountLive } = await import("./live.js");
    if (!closed) {
      dispose = mountLive(view, {
        fetchSnapshot: () => api("GET", "live/snapshot"),
        fetchStats: () => api("GET", "live/stats"),
        fetchFriends: (id) => api("GET", "live/users/" + encodeURIComponent(id) + "/friends"),
        onEvent: (handler) => { state.onEvent = handler; },
        connected: () => state.events?.readyState === EventSource.OPEN,
        errText,
      });
    }
  } catch (e) {
    if (!closed) placeholder.textContent = "实况页面加载失败";
  }
}

// ---------- live events ----------

function openEvents() {
  closeEvents();
  const es = new EventSource("api/events");
  state.events = es;
  es.onopen = () => {
    $("#live").classList.add("on");
    state.onEvent?.({ type: "stream.open" });
  };
  es.onerror = async () => {
    $("#live").classList.remove("on");
    state.onEvent?.({ type: "stream.closed" });
    // The browser reconnects by itself; check whether access is gone so the
    // page does not keep retrying a closed door.
    if (es.readyState === EventSource.CLOSED || state.events !== es) return;
    try {
      const me = await api("GET", "me");
      if (state.events === es && state.me && me.perms.join() !== state.me.perms.join()) {
        state.me = me;
        enterShell();
      }
    } catch { /* api() already handled it */ }
  };
  es.onmessage = (m) => {
    let ev;
    try { ev = JSON.parse(m.data); } catch { return; }
    if (state.onEvent) state.onEvent(ev);
  };
}

function closeEvents() {
  if (state.events) { state.events.close(); state.events = null; }
  $("#live").classList.remove("on");
}

// ---------- overview ----------

async function renderOverview(view) {
  view.append(el("h2", {}, "概览"));
  const body = el("div");
  view.append(body);
  let timer = 0;
  const load = async () => {
    try { draw(await api("GET", "stats")); } catch (e) { body.replaceChildren(el("p", { class: "error" }, errText(e))); }
  };
  // Presence and messages change the numbers: reload, at most once a second.
  state.onEvent = (ev) => {
    if (["online", "offline", "message", "registered"].includes(ev.type) && !timer) {
      timer = setTimeout(() => { timer = 0; load(); }, 1000);
    }
  };

  function draw(s) {
    const card = (k, v, small) => el("div", { class: "card" }, el("div", { class: "k" }, k), el("div", { class: "v" + (small ? " small" : "") }, v));
    const users = new Set(s.online.map((o) => o.user_id));
    const max = Math.max(1, ...s.versions.map((v) => v.count));
    body.replaceChildren(
      el("div", { class: "cards" },
        card("服务端版本", s.server_version, true),
        card("运行时长", fmtDuration(s.uptime_seconds), true),
        card("在线用户", users.size),
        card("连接数", s.connections),
        card("用户", s.counts.users),
        card("频道", s.counts.channels),
        card("私聊", s.counts.dms),
        card("消息总数", s.counts.messages),
        card("近 24 小时消息", s.messages_24h),
        card("内存", fmtBytes(s.memory_bytes), true),
        card("协程", s.goroutines),
        card("最低客户端版本", s.min_client_version || "不限制", true),
      ),
      el("div", { class: "grid2" },
        el("section", {},
          el("h3", {}, `在线（${s.online.length} 个连接）`),
          s.online.length ? el("table", {},
            el("thead", {}, el("tr", {}, el("th", {}, "用户"), el("th", {}, "客户端版本"), el("th", { class: "opt" }, "来源"), el("th", { class: "opt" }, "连接于"))),
            el("tbody", {}, s.online.map((o) => el("tr", {},
              el("td", {}, o.username),
              el("td", { class: "mono" }, o.client_version === "unknown" ? "未知" : o.client_version),
              el("td", { class: "opt mono faint" }, o.remote),
              el("td", { class: "opt faint" }, fmtShort(o.since)))))) : el("p", { class: "empty" }, "没有人在线")),
        el("section", {},
          el("h3", {}, "在线客户端版本分布"),
          s.versions.length ? el("table", {},
            el("tbody", {}, s.versions.map((v) => el("tr", {},
              el("td", { class: "mono" }, v.version === "unknown" ? "未知" : v.version),
              el("td", { style: "width:55%" }, el("div", { class: "bar", style: `width:${(v.count / max) * 100}%` })),
              el("td", {}, v.count))))) : el("p", { class: "empty" }, "没有在线客户端"))),
      el("h3", {}, `最近日志（${s.logs.length} 条）`),
      el("div", { class: "logs" }, s.logs.slice().reverse().map((l) => {
        const lvl = (l.match(/^\S+ (DBG|INF|WRN|ERR)/) || [])[1] || "";
        return el("div", { class: lvl }, l);
      })),
    );
  }
  await load();
}

// ---------- users ----------

function userTags(u) {
  return [
    u.online && el("span", { class: "tag on" }, "在线"),
    u.ban && el("span", { class: "tag ban", title: u.ban.reason }, "封禁至 " + fmtUntil(u.ban.expires_at)),
    u.mute && el("span", { class: "tag mute", title: u.mute.reason }, "禁言至 " + fmtUntil(u.mute.expires_at)),
    u.super ? el("span", { class: "tag admin" }, "超级管理员") : u.admin && el("span", { class: "tag admin" }, "管理员"),
  ];
}

async function renderUsers(view, [id]) {
  if (id) return renderUserDetail(view, id);
  view.append(el("h2", {}, "用户"));
  const q = el("input", { type: "search", placeholder: "按用户名筛选" });
  const table = el("div");
  const prev = el("button", { type: "button", disabled: true }, "上一页");
  const next = el("button", { type: "button", disabled: true }, "下一页");
  view.append(el("div", { class: "toolbar" }, q, prev, next), table);

  let after = "", nextCursor = "", revision = 0, timer;
  const history = [];
  const draw = (rows) => {
    table.replaceChildren(rows.length ? el("table", {},
      el("thead", {}, el("tr", {}, el("th", {}, "用户名"), el("th", {}, "状态"), el("th", { class: "opt" }, "客户端"), el("th", { class: "opt" }, "注册于"))),
      el("tbody", {}, rows.map((u) => el("tr", { class: "click", onclick: () => { location.hash = "users/" + encodeURIComponent(u.id); } },
        el("td", {}, u.username),
        el("td", {}, userTags(u)),
        el("td", { class: "opt mono" }, (u.client_versions || []).map((v) => v === "unknown" ? "未知" : v).join(", ")),
        el("td", { class: "opt faint" }, fmtTime(u.created_at)))))) : el("p", { class: "empty" }, "没有匹配的用户"));
  };
  const load = async () => {
    const current = ++revision;
    const params = new URLSearchParams({ limit: "50", q: q.value.trim(), after });
    prev.disabled = next.disabled = true;
    try {
      const page = await api("GET", "users?" + params);
      if (current !== revision) return;
      nextCursor = page.next || "";
      draw(page.users);
    } catch (e) {
      if (current !== revision) return;
      nextCursor = "";
      table.replaceChildren(el("p", { class: "error" }, errText(e)));
    }
    prev.disabled = history.length === 0;
    next.disabled = !nextCursor;
  };
  const schedule = () => {
    if (timer) return;
    timer = setTimeout(() => {
      timer = undefined;
      if (view.isConnected) load();
    }, 200);
  };
  q.addEventListener("input", () => {
    revision++;
    prev.disabled = next.disabled = true;
    after = "";
    history.length = 0;
    schedule();
  });
  prev.addEventListener("click", () => {
    after = history.pop() || "";
    load();
  });
  next.addEventListener("click", () => {
    history.push(after);
    after = nextCursor;
    load();
  });
  state.onEvent = (ev) => { if (["online", "offline", "registered", "sanction"].includes(ev.type)) schedule(); };
  await load();
}

const DURATIONS = [
  ["60", "1 小时"], ["1440", "1 天"], ["10080", "7 天"], ["43200", "30 天"], ["custom", "自定义"], ["0", "永久"],
];

function sanctionForm(kind) {
  const reason = el("textarea", { name: "reason", rows: 3, required: true, maxlength: 500, placeholder: "必填，会显示给对方" });
  const dur = el("select", { name: "duration" }, DURATIONS.map(([v, l]) => el("option", { value: v }, l)));
  const custom = el("input", { name: "custom", type: "number", min: 1, step: 1, value: 3, hidden: true });
  const unit = el("select", { name: "unit", hidden: true }, el("option", { value: "60" }, "小时"), el("option", { value: "1440" }, "天"));
  dur.addEventListener("change", () => { custom.hidden = unit.hidden = dur.value !== "custom"; });
  return {
    body: [
      el("label", {}, kind === "ban" ? "封禁原因" : "禁言原因", reason),
      el("label", {}, "时长", el("div", { class: "toolbar" }, dur, custom, unit)),
      kind === "ban" ? el("p", { class: "muted" }, "封禁会立即断开对方的所有连接，并让已保存的登录失效。") : el("p", { class: "muted" }, "禁言期间可以登录和阅读，但不能发消息、编辑消息或建频道。"),
    ],
    values() {
      if (!reason.value.trim()) throw new Error("请填写原因");
      let minutes = Number(dur.value);
      if (dur.value === "custom") {
        const n = Math.floor(Number(custom.value));
        if (!(n >= 1)) throw new Error("请输入有效的时长");
        minutes = n * Number(unit.value);
      }
      return { reason: reason.value.trim(), minutes };
    },
  };
}

async function renderUserDetail(view, id) {
  view.append(el("p", {}, el("a", { href: "#users" }, "← 全部用户")));
  const box = el("div");
  view.append(box);
  const load = async () => {
    let u;
    try { u = await api("GET", "users/" + encodeURIComponent(id)); } catch (e) { box.replaceChildren(el("p", { class: "error" }, errText(e))); return; }
    const self = u.id === state.me.id;
    const protectedUser = u.super || self;
    const act = async (label, fn) => { try { await fn(); toast(label); load(); } catch (e) { toast(errText(e), true); } };
    const sanction = (kind) => async () => {
      const f = sanctionForm(kind);
      const ok = await modal(kind === "ban" ? `封禁 ${u.username}` : `禁言 ${u.username}`, f.body, kind === "ban" ? "封禁" : "禁言",
        () => api("POST", `users/${encodeURIComponent(u.id)}/${kind}`, f.values()), true);
      if (ok) { toast(kind === "ban" ? "已封禁" : "已禁言"); load(); }
    };
    const resetPw = async () => {
      const p1 = el("input", { type: "password", autocomplete: "new-password", required: true, minlength: 8 });
      const p2 = el("input", { type: "password", autocomplete: "new-password", required: true });
      const ok = await modal(`重置 ${u.username} 的密码`, [
        el("label", {}, "新密码（至少 8 个字符）", p1), el("label", {}, "再输入一次", p2),
        el("p", { class: "muted" }, "对方的所有登录会立即失效，需要用新密码重新登录。"),
      ], "重置", () => {
        if (p1.value !== p2.value) throw new Error("两次输入的密码不一致");
        return api("POST", `users/${encodeURIComponent(u.id)}/reset-password`, { password: p1.value });
      }, true);
      if (ok) toast("密码已重置");
    };
    const kick = async () => {
      const ok = await modal(`让 ${u.username} 下线`, el("p", {}, "对方会立即下线，但可以马上重新登录。这不是处分。"), "下线",
        () => api("POST", `users/${encodeURIComponent(u.id)}/kick`, {}), true);
      if (ok) { toast("已下线"); load(); }
    };
    const revoke = (sc) => act("已解除", () => api("POST", `users/${encodeURIComponent(u.id)}/revoke`, { sanction_id: sc.id }));

    box.replaceChildren(
      el("h2", {}, u.username, " ", userTags(u)),
      el("dl", { class: "kv" },
        el("dt", {}, "注册于"), el("dd", {}, fmtTime(u.created_at)),
        el("dt", {}, "在线客户端"), el("dd", { class: "mono" }, u.online ? u.client_versions.map((v) => v === "unknown" ? "未知" : v).join(", ") : "离线"),
        el("dt", {}, "管理权限"), el("dd", {}, u.super ? "全部（超级管理员）" : u.perms.length ? u.perms.map((p) => PERM_LABEL[p] || p).join("、") : "无"),
        el("dt", {}, "自助找回"), el("dd", {}, u.can_recover ? "已设置安全问题" : "未设置，只能由管理员重置密码")),
      protectedUser ? el("p", { class: "muted" }, self ? "不能处置自己。" : "超级管理员不能被处置。") : el("div", { class: "detail-actions" },
        el("button", { class: "danger", onclick: sanction("ban") }, u.ban ? "重新封禁" : "封禁"),
        el("button", { class: "secondary", onclick: sanction("mute") }, u.mute ? "重新禁言" : "禁言"),
        u.ban && el("button", { class: "secondary", onclick: () => revoke(u.ban) }, "解除封禁"),
        u.mute && el("button", { class: "secondary", onclick: () => revoke(u.mute) }, "解除禁言"),
        el("button", { class: "secondary", onclick: resetPw }, "重置密码"),
        u.online && el("button", { class: "secondary", onclick: kick }, "强制下线")),
      el("h3", {}, "当前会话来源"),
      u.sessions.length ? el("table", {},
        el("thead", {}, el("tr", {}, el("th", {}, "类型"), el("th", {}, "来源"), el("th", {}, "设备"), el("th", {}, "客户端版本"), el("th", { class: "opt" }, "签发于"), el("th", { class: "opt" }, "过期于"))),
        el("tbody", {}, u.sessions.map((s) => el("tr", {},
          el("td", {}, s.purpose === "admin" ? "管理后台" : "聊天"),
          el("td", { class: "mono" }, s.remote),
          el("td", {}, s.device),
          el("td", { class: "mono" }, s.client_version === "unknown" ? "未知" : s.client_version || "—"),
          el("td", { class: "opt faint" }, fmtTime(s.created_at)),
          el("td", { class: "opt faint" }, fmtTime(s.expires_at)))))) : el("p", { class: "empty" }, "没有有效的登录"),
      el("h3", {}, "处分历史"),
      u.sanctions.length ? el("table", {},
        el("thead", {}, el("tr", {}, el("th", {}, "类型"), el("th", {}, "原因"), el("th", {}, "时间"), el("th", {}, "到期"), el("th", {}, "状态"), el("th", { class: "opt" }, "操作人"))),
        el("tbody", {}, u.sanctions.map((sc) => el("tr", {},
          el("td", {}, sc.kind === "ban" ? "封禁" : "禁言"),
          el("td", {}, sc.reason),
          el("td", { class: "faint" }, fmtTime(sc.created_at)),
          el("td", { class: "faint" }, fmtUntil(sc.expires_at)),
          el("td", {}, sc.active ? el("span", { class: "tag " + sc.kind }, "生效中")
            : sc.revoked_at ? `已由 ${sc.revoked_by_name} 解除（${fmtTime(sc.revoked_at)}）`
            : sc.superseded_by ? "被新处分取代" : "已到期"),
          el("td", { class: "opt" }, sc.created_by_name))))) : el("p", { class: "empty" }, "没有处分记录"),
    );
  };
  state.onEvent = (ev) => {
    if (ev.data && ev.data.user_id === id && ["online", "offline", "sanction"].includes(ev.type)) load();
  };
  await load();
}

// ---------- conversations ----------

async function renderChats(view, [openId]) {
  view.className = "view split";
  const filter = el("input", { type: "search", placeholder: "筛选会话" });
  const list = el("div");
  const pane = el("div", { class: "pane" }, el("div", { class: "empty" }, "选择左侧的会话查看消息"));
  view.append(el("div", { class: "split-grid" }, el("div", { class: "convs" }, filter, list), pane));

  let convs = [];
  let current = null; // {id, oldest, hasMore, box}
  const drawList = () => {
    const f = filter.value.trim().toLowerCase();
    list.replaceChildren(...convs.filter((c) => !f || c.label.toLowerCase().includes(f)).map((c) =>
      el("div", { class: "conv" + (current && current.id === c.id ? " on" : ""), onclick: () => { location.hash = "chats/" + encodeURIComponent(c.id); } },
        el("div", { class: "l" }, el("span", { class: "n" }, c.kind === "dm" ? "🔒 " + c.label : c.label), el("span", { class: "t" }, fmtShort(c.last_at))),
        el("div", { class: "faint", style: "font-size:12px" }, `${c.message_count} 条消息 · ${c.members} 人`))));
  };
  filter.addEventListener("input", drawList);

  const msgNode = (m, fresh) => el("div", { class: "msg" + (m.deleted ? " del" : "") + (fresh ? " new" : ""), "data-id": m.id },
    el("div", { class: "h" }, el("span", { class: "a" }, m.author_name), el("span", { class: "tm" }, fmtTime(m.created_at)),
      m.edited_at && !m.deleted ? el("span", { class: "tm" }, "已编辑") : null),
    el("div", { class: "b" }, m.deleted ? "（消息已删除）" : m.body));

  async function open(id) {
    current = { id, oldest: 0, hasMore: false, box: el("div", { class: "msgs" }) };
    drawList();
    let page;
    try { page = await api("GET", `conversations/${encodeURIComponent(id)}/messages`); } catch (e) {
      pane.replaceChildren(el("p", { class: "error" }, errText(e))); return;
    }
    const c = page.conversation;
    pane.replaceChildren(
      el("div", { class: "pane-head" }, el("div", {}, c.kind === "dm" ? "私聊 " + c.label : c.label),
        el("div", { class: "sub" }, (c.topic ? c.topic + " · " : "") + "查看会记入审计日志")),
      current.box);
    addPage(page, true);
    current.box.addEventListener("scroll", () => { if (current.box.scrollTop < 40) older(); });
  }

  function addPage(page, initial) {
    const box = current.box;
    const before = box.scrollHeight - box.scrollTop;
    const nodes = page.messages.map((m) => msgNode(m, false));
    $(".more", box)?.remove();
    box.prepend(...nodes);
    current.hasMore = page.has_more;
    if (page.messages.length) current.oldest = page.messages[0].seq;
    if (current.hasMore) box.prepend(el("div", { class: "more faint" }, "向上滚动加载更早的消息"));
    else box.prepend(el("div", { class: "more faint" }, "会话开头"));
    if (!page.messages.length && initial) box.append(el("p", { class: "empty" }, "还没有消息"));
    box.scrollTop = initial ? box.scrollHeight : box.scrollHeight - before;
  }

  let loading = false;
  async function older() {
    if (!current || !current.hasMore || loading) return;
    loading = true;
    try { addPage(await api("GET", `conversations/${encodeURIComponent(current.id)}/messages?before=${current.oldest}`), false); }
    catch (e) { toast(errText(e), true); }
    finally { loading = false; }
  }

  state.onEvent = (ev) => {
    if (!ev.type.startsWith("message") || !ev.data || !ev.data.message) return;
    const m = ev.data.message;
    const c = convs.find((x) => x.id === m.conversation_id);
    if (c && ev.type === "message") { c.last_at = m.created_at; c.message_count = m.seq; convs.sort((a, b) => b.last_at - a.last_at); drawList(); }
    if (!c) loadList();
    if (!current || current.id !== m.conversation_id) return;
    const box = current.box;
    const existing = box.querySelector(`[data-id="${CSS.escape(m.id)}"]`);
    if (existing) { existing.replaceWith(msgNode(m, true)); return; }
    if (ev.type !== "message") return;
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
    $(".empty", box)?.remove();
    box.append(msgNode(m, true));
    if (atBottom) box.scrollTop = box.scrollHeight;
  };

  async function loadList() {
    try { convs = (await api("GET", "conversations")).conversations; drawList(); } catch (e) { list.replaceChildren(el("p", { class: "error" }, errText(e))); }
  }
  await loadList();
  if (openId) await open(openId);
}

// ---------- search ----------

function dateMs(v, endOfDay) {
  if (!v) return "";
  const d = new Date(v + (endOfDay ? "T23:59:59.999" : "T00:00:00"));
  return isNaN(d) ? "" : String(d.getTime());
}

async function renderSearch(view) {
  view.append(el("h2", {}, "搜索消息"));
  const q = el("input", { type: "search", name: "q", placeholder: "关键词" });
  const author = el("input", { name: "author", placeholder: "用户名" });
  const conv = el("select", { name: "conversation" }, el("option", { value: "" }, "全部会话"));
  const from = el("input", { type: "date" });
  const to = el("input", { type: "date" });
  const go = el("button", { type: "submit" }, "搜索");
  const results = el("div");
  const form = el("form", { class: "toolbar" },
    el("label", {}, "关键词", q), el("label", {}, "作者", author), el("label", {}, "会话", conv),
    el("label", {}, "从", from), el("label", {}, "到", to), go);
  view.append(form, el("p", { class: "muted" }, "每次搜索都会连同搜索条件记入审计日志。"), results);

  api("GET", "conversations").then((r) => {
    conv.append(...r.conversations.map((c) => el("option", { value: c.id }, (c.kind === "dm" ? "私聊 " : "") + c.label)));
  }).catch(() => {});

  let params = null, next = null, count = 0;
  const table = el("tbody");
  async function run(more) {
    if (!more) {
      params = new URLSearchParams();
      if (q.value.trim()) params.set("q", q.value.trim());
      if (author.value.trim()) params.set("author", author.value.trim());
      if (conv.value) params.set("conversation", conv.value);
      if (from.value) params.set("from", dateMs(from.value, false));
      if (to.value) params.set("to", dateMs(to.value, true));
      if (![...params.keys()].length) { toast("至少填写一个条件", true); return; }
      table.replaceChildren();
      count = 0;
    }
    const p = new URLSearchParams(params);
    if (more && next) { p.set("cursor_at", next.at); p.set("cursor_id", next.id); }
    go.disabled = true;
    let r;
    try { r = await api("GET", "search?" + p); } catch (e) { toast(errText(e), true); return; } finally { go.disabled = false; }
    next = r.next;
    count += r.messages.length;
    table.append(...r.messages.map((m) => el("tr", { class: "click", onclick: () => { location.hash = "chats/" + encodeURIComponent(m.conversation_id); } },
      el("td", { class: "faint" }, fmtTime(m.created_at)),
      el("td", {}, (m.conversation_kind === "dm" ? "🔒 " : "") + m.conversation_label),
      el("td", {}, m.author_name),
      el("td", { class: "b" }, m.body))));
    results.replaceChildren(
      count ? el("table", {}, el("thead", {}, el("tr", {}, el("th", {}, "时间"), el("th", {}, "会话"), el("th", {}, "作者"), el("th", {}, "内容"))), table)
        : el("p", { class: "empty" }, next ? "这一批没有匹配，继续搜索更早的消息" : "没有匹配的消息"),
      el("p", { class: "more" }, next ? el("button", { class: "secondary", onclick: () => run(true) }, `继续搜索（已找到 ${count} 条）`)
        : count ? el("span", { class: "faint" }, `共 ${count} 条，已搜完`) : null));
  }
  form.addEventListener("submit", (ev) => { ev.preventDefault(); run(false); });
  q.focus();
}

// ---------- moments ----------

// renderMoments lists every moment, private ones included, newest first. Each
// one and each comment can be deleted by someone who may also moderate users
// (spec admin-console / 动态浏览与删除).
async function renderMoments(view) {
  view.append(el("h2", {}, "朋友圈动态"));
  const author = el("input", { name: "author", placeholder: "作者用户名" });
  const go = el("button", { type: "submit" }, "筛选");
  const form = el("form", { class: "toolbar" }, el("label", {}, "作者", author), go);
  const canDelete = can("users.moderate");
  const list = el("div");
  const more = el("p", { class: "more" });
  view.append(form, ...(canDelete ? [] : [el("p", { class: "muted" }, "删除动态和评论还需要「处置用户」权限。")]), list, more);

  let next = null;
  const who = (u) => (u.nickname ? `${u.nickname}（@${u.username}）` : u.username);

  function card(m) {
    const box = el("div", { class: "msg moment" });
    const comments = el("div", { class: "comments" });
    for (const c of m.comments) {
      comments.append(el("div", { class: "comment" },
        el("span", { class: "a" }, who(c.author)),
        c.reply_to ? el("span", { class: "faint" }, ` 回复 ${who(c.reply_to)}`) : null,
        "：", el("span", { class: "b" }, c.body), " ",
        el("span", { class: "tm" }, fmtShort(c.created_at)),
        canDelete ? el("button", { class: "link danger", onclick: () => del("comments", c.id, `删除 ${c.author.username} 的评论？`, c.body) }, "删除") : null));
    }
    box.append(...[
      el("div", { class: "h" },
        el("span", { class: "a" }, who(m.author)),
        el("span", { class: "tm" }, fmtTime(m.created_at)),
        m.visibility === "private" ? el("span", { class: "tag" }, "仅自己可见") : el("span", { class: "tag on" }, "好友可见"),
        el("span", { class: "faint" }, `赞 ${m.likes.length} · 评论 ${m.comments.length}`),
        canDelete ? el("button", { class: "link danger", onclick: () => del("moments", m.id, `删除 ${m.author.username} 的动态？`, m.body) }, "删除动态") : null),
      el("div", { class: "b" }, m.body),
      m.comments.length ? comments : null].filter(Boolean));
    return box;
  }

  async function del(kind, id, title, body) {
    const ok = await modal(title, [el("p", { class: "b" }, body), el("p", { class: "muted" }, "删除后所有人都看不到，并记入审计日志。")],
      "删除", () => api("POST", `${kind}/${encodeURIComponent(id)}/delete`, {}), true);
    if (ok) { toast("已删除"); load(false); }
  }

  async function load(append) {
    const p = new URLSearchParams();
    if (author.value.trim()) p.set("author", author.value.trim());
    if (append && next) { p.set("cursor_at", next.at); p.set("cursor_id", next.id); }
    let r;
    try { r = await api("GET", "moments?" + p); } catch (e) { list.replaceChildren(el("p", { class: "error" }, errText(e))); return; }
    if (!append) list.replaceChildren();
    list.append(...r.moments.map(card));
    if (!list.children.length) list.append(el("p", { class: "empty" }, "没有动态"));
    next = r.next;
    more.replaceChildren(...(next ? [el("button", { class: "secondary", onclick: () => load(true) }, "加载更早的")] : []));
  }
  form.addEventListener("submit", (ev) => { ev.preventDefault(); load(false); });
  await load(false);
}

// ---------- accounts ----------

// grantable mirrors the server's rules so the form disables what would be
// refused, and says why. The server enforces them regardless.
function grantable(perm, target) {
  if (!state.me.super && perm === "accounts.manage") return "只有超级管理员能授予或收回";
  if (target && target.perms.includes(perm)) return "";
  if (!can(perm)) return "你自己没有这项权限";
  return "";
}

function permChecks(target) {
  return el("div", { class: "checks" }, state.me.all_perms.map((p) => {
    const why = grantable(p, target);
    return el("label", {},
      el("input", { type: "checkbox", value: p, checked: !!(target && target.perms.includes(p)), disabled: !!why }),
      PERM_LABEL[p] || p, why ? el("span", { class: "why" }, `（${why}）`) : null);
  }));
}
const checkedPerms = (box) => [...box.querySelectorAll("input:checked")].map((i) => i.value);

async function renderAccounts(view) {
  view.append(el("div", { class: "toolbar" }, el("h2", { style: "margin:0;flex:1" }, "账号与权限"),
    el("button", { onclick: create }, "新建账号")));
  const box = el("div");
  view.append(box);

  async function create() {
    const name = el("input", { autocomplete: "off", required: true, minlength: 3, maxlength: 32 });
    const pw1 = el("input", { type: "password", autocomplete: "new-password", required: true, minlength: 8 });
    const checks = permChecks(null);
    const ok = await modal("新建账号", [
      el("label", {}, "用户名", name), el("label", {}, "初始密码（至少 8 个字符）", pw1),
      el("div", {}, el("div", { class: "muted", style: "margin-bottom:6px" }, "管理权限（不勾选即普通聊天账号）"), checks),
      el("p", { class: "muted" }, "新账号没有安全问题，忘记密码时只能由管理员重置。"),
    ], "创建", () => api("POST", "accounts", { username: name.value.trim(), password: pw1.value, perms: checkedPerms(checks) }));
    if (ok) { toast("已创建 " + ok.username); load(); }
  }

  async function edit(u) {
    const checks = permChecks(u);
    const ok = await modal(`${u.username} 的权限`, checks, "保存",
      () => api("POST", `users/${encodeURIComponent(u.id)}/perms`, { perms: checkedPerms(checks) }));
    if (ok) { toast("已更新"); load(); }
  }

  async function load() {
    let users;
    try { users = (await api("GET", "accounts")).users; } catch (e) { box.replaceChildren(el("p", { class: "error" }, errText(e))); return; }
    const admins = users.filter((u) => u.admin), others = users.filter((u) => !u.admin);
    const row = (u) => el("tr", {},
      el("td", {}, u.username, " ", u.super ? el("span", { class: "tag admin" }, "超级管理员") : null),
      el("td", {}, u.super ? "全部" : u.perms.map((p) => PERM_LABEL[p] || p).join("、") || el("span", { class: "faint" }, "无")),
      el("td", {}, u.super || u.id === state.me.id ? el("span", { class: "faint" }, u.id === state.me.id ? "不能改自己" : "")
        : el("button", { class: "link", onclick: () => edit(u) }, "修改权限")));
    const table = (rows) => el("table", {}, el("thead", {}, el("tr", {}, el("th", {}, "用户名"), el("th", {}, "权限"), el("th", {}))), el("tbody", {}, rows.map(row)));
    box.replaceChildren(
      el("h3", {}, `管理员（${admins.length}）`), table(admins),
      el("h3", {}, `其他账号（${others.length}）`), others.length ? table(others) : el("p", { class: "empty" }, "没有其他账号"));
  }
  await load();
}

// ---------- audit ----------

const ACTION_LABEL = {
  admin_login: "登录后台", admin_logout: "退出后台",
  view_conversation: "查看会话", search: "搜索消息",
  ban: "封禁", mute: "禁言", revoke: "解除处分",
  reset_password: "重置密码", kick: "强制下线",
  create_account: "新建账号", set_perms: "修改权限",
  create_admin: "命令行创建超级管理员", promote_admin: "命令行提升为超级管理员",
  delete_moment: "删除动态", delete_comment: "删除评论",
};

function auditDetail(e) {
  const d = e.detail || {};
  switch (e.action) {
    case "view_conversation": return (d.kind === "dm" ? "私聊 " : "") + (d.label || e.target_name || e.target) + (d.before ? `（更早于 #${d.before}）` : "");
    case "search": return Object.entries(d).filter(([k]) => k !== "continued").map(([k, v]) => {
      if (k === "from" || k === "to") v = fmtTime(Number(v));
      return `${{ q: "关键词", author: "作者", conversation: "会话", from: "从", to: "到" }[k] || k}: ${v}`;
    }).join("，") + (d.continued ? "（继续）" : "");
    case "ban": case "mute": return `${d.username}，原因：${d.reason}，至 ${d.until === "permanent" ? "永久" : fmtTime(Date.parse(d.until))}`;
    case "revoke": return `${d.username} 的${d.kind === "ban" ? "封禁" : "禁言"}`;
    case "set_perms": return `${d.username}：${d.before || "无"} → ${d.after || "无"}`;
    case "create_account": return `${d.username}${d.perms ? "，权限 " + d.perms : ""}${d.super === "true" ? "，超级管理员" : ""}`;
    case "admin_login": return d.addr || "";
    case "delete_moment": case "delete_comment": return `${d.username}：${d.body}`;
    default: return d.username || e.target_name || "";
  }
}

async function renderAudit(view) {
  view.append(el("h2", {}, "审计日志"));
  const actor = el("input", { placeholder: "操作人用户名" });
  const action = el("select", {}, el("option", { value: "" }, "全部操作"),
    Object.entries(ACTION_LABEL).map(([k, v]) => el("option", { value: k }, v)));
  const from = el("input", { type: "date" }), to = el("input", { type: "date" });
  const form = el("form", { class: "toolbar" },
    el("label", {}, "操作人", actor), el("label", {}, "操作", action), el("label", {}, "从", from), el("label", {}, "到", to),
    el("button", { type: "submit" }, "筛选"));
  const tbody = el("tbody");
  const more = el("p", { class: "more" });
  view.append(form, el("table", {}, el("thead", {}, el("tr", {}, el("th", {}, "时间"), el("th", {}, "操作人"), el("th", {}, "操作"), el("th", {}, "详情"))), tbody), more);

  let before = 0;
  async function load(reset) {
    if (reset) { before = 0; tbody.replaceChildren(); }
    const p = new URLSearchParams({ limit: "100" });
    if (actor.value.trim()) p.set("actor_name", actor.value.trim());
    if (action.value) p.set("action", action.value);
    if (from.value) p.set("from", dateMs(from.value, false));
    if (to.value) p.set("to", dateMs(to.value, true));
    if (before) p.set("before", before);
    let r;
    try { r = await api("GET", "audit?" + p); } catch (e) { more.replaceChildren(el("span", { class: "error" }, errText(e))); return; }
    tbody.append(...r.entries.map((e) => el("tr", {},
      el("td", { class: "faint" }, fmtTime(e.at)),
      el("td", {}, e.actor_name),
      el("td", {}, ACTION_LABEL[e.action] || e.action),
      el("td", {}, auditDetail(e)))));
    if (r.entries.length) before = r.entries[r.entries.length - 1].at;
    more.replaceChildren(r.entries.length === 100 ? el("button", { class: "secondary", onclick: () => load(false) }, "加载更早的记录")
      : tbody.children.length ? el("span", { class: "faint" }, "没有更早的记录") : el("span", { class: "empty" }, "没有记录"));
  }
  form.addEventListener("submit", (ev) => { ev.preventDefault(); load(true); });
  await load(true);
}

// ---------- start ----------

(async function start() {
  try {
    state.me = await api("GET", "me");
    enterShell();
  } catch {
    if (!state.me) showLogin();
  }
})();
