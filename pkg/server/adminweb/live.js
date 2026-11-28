import { LiveScene } from "./live-scene.js";

const categories = new Map([["消息", "message"], ["社交", "social"], ["账号", "account"], ["系统", "system"]]);
const activityColors = ["#7fb4ff", "#61e7e4", "#8f91fb", "#c88bfd", "#eea6f0", "#f48d8d",
  "#ffbb7f", "#ebd877", "#71b6a8", "#bcdd91", "#fc95bc", "#ff6965", "#eddfc3", "#7bee9c", "#6da6aa", "#a9bcdc", "#eaffab"];
const defaultChartSeries = ["dm", "channel_message", "comment", "like", "online", "offline"];
const maxChartMinutes = 60;
const activityClock = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3, hourCycle: "h23",
});
const defaultUsersLimit = 240, defaultChannelsLimit = 48, maxSceneLimit = 10000, optionsLimit = 80;
const channelRefillDelay = 1450;

function create(tag, className, text = "") {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text) node.textContent = text;
  return node;
}

function drawActivityChart(canvas, trend, series, hover) {
  const width = canvas.clientWidth, height = canvas.clientHeight;
  if (!width || !height) return;
  const scale = Math.min(devicePixelRatio || 1, 2);
  const pixels = [Math.round(width * scale), Math.round(height * scale)];
  if (canvas.width !== pixels[0]) canvas.width = pixels[0];
  if (canvas.height !== pixels[1]) canvas.height = pixels[1];
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.setTransform(scale, 0, 0, scale, 0, 0);
  ctx.clearRect(0, 0, width, height);
  if (!trend?.samples.length || !series.length) return;
  const samples = trend.samples;
  let maxCount = 4;
  for (const sample of samples) {
    for (const item of series) maxCount = Math.max(maxCount, sample.counts[item.index]);
  }
  const step = Math.ceil(maxCount / 4);
  const left = Math.min(width / 3, String(step * 4).length * 6 + 12);
  const right = width - 7, top = 7, bottom = height - 18;
  if (right <= left || bottom <= top) return;
  ctx.lineWidth = 1;
  ctx.font = "10px monospace";
  ctx.fillStyle = "#a1b5c5";
  ctx.strokeStyle = "#294452";
  for (let i = 0; i <= 4; i++) {
    const y = bottom - (bottom - top) * i / 4;
    ctx.beginPath(); ctx.moveTo(left, y); ctx.lineTo(right, y); ctx.stroke();
    ctx.textAlign = "right";
    ctx.fillText(String(step * i), left - 5, y + 3);
  }
  ctx.textAlign = "left"; ctx.fillText(`−${trend.minutes} 分`, left, height - 3);
  ctx.textAlign = "right"; ctx.fillText("现在", right, height - 3);
  const xAt = (i) => left + (right - left) * i / Math.max(1, samples.length - 1);
  const yAt = (value) => bottom - (bottom - top) * value / (step * 4);
  const pointsPerPixel = Math.max(1, Math.ceil(samples.length / (right - left)));
  for (const { color, index } of series) {
    ctx.beginPath();
    for (let i = 0; i < samples.length; i += pointsPerPixel) {
      // When several seconds share one pixel, keep the peak visible.
      let peak = i;
      for (let j = i + 1; j < Math.min(samples.length, i + pointsPerPixel); j++) {
        if (samples[j].counts[index] > samples[peak].counts[index]) peak = j;
      }
      const x = xAt(peak), y = yAt(samples[peak].counts[index]);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = color;
    ctx.lineWidth = 1.6;
    ctx.stroke();
  }
  if (hover !== null) {
    ctx.strokeStyle = "#7496a5";
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(xAt(hover), top); ctx.lineTo(xAt(hover), bottom); ctx.stroke();
    for (const { color, index } of series) {
      ctx.fillStyle = color;
      ctx.beginPath(); ctx.arc(xAt(hover), yAt(samples[hover].counts[index]), 2, 0, Math.PI * 2); ctx.fill();
    }
  }
  return { left, right };
}

function activityChart(onRangeChange) {
  const chart = create("section", "live-chart");
  const title = create("h3", "", "活动曲线");
  const filters = create("details", "live-trend-filters");
  const filterSummary = create("summary", "", "分类");
  const filterPanel = create("div", "live-trend-options");
  const checklist = create("div", "live-trend-checklist");
  const presets = create("div", "live-trend-presets");
  const number = create("input", "");
  const minutesLabel = create("label", "live-trend-minutes");
  const interval = create("span", "live-trend-interval", "加载中");
  const legend = create("div", "live-chart-legend");
  const canvas = create("canvas", "");
  canvas.setAttribute("role", "img");
  number.type = "number";
  number.min = "1";
  number.max = String(maxChartMinutes);
  number.step = "1";
  number.setAttribute("aria-label", "活动曲线分钟数");
  minutesLabel.append(number, document.createTextNode("分"));
  let minutes = 1, selected = new Set(defaultChartSeries), data = null, hover = null, layout;
  try {
    const saved = Number(localStorage.getItem("link-chat-live-trend-minutes"));
    if (Number.isInteger(saved) && saved >= 1 && saved <= maxChartMinutes) minutes = saved;
    const series = JSON.parse(localStorage.getItem("link-chat-live-trend-series"));
    if (Array.isArray(series)) selected = new Set(series);
  } catch { /* Browser storage is optional. */ }
  number.value = String(minutes);
  const presetButtons = [1, 5, 15, 30, 60].map((value) => {
    const button = create("button", "", String(value));
    button.type = "button";
    button.setAttribute("aria-label", `最近 ${value} 分钟`);
    button.onclick = () => setMinutes(value);
    presets.append(button);
    return [value, button];
  });
  for (const [label, all] of [["全选", true], ["恢复默认", false]]) {
    const button = create("button", "", label);
    button.type = "button";
    button.onclick = () => {
      selected = new Set(all && checks.size ? checks.keys() : defaultChartSeries);
      applySelection();
    };
    filterPanel.append(button);
  }
  filterPanel.append(checklist);
  filters.append(filterSummary, filterPanel);
  const head = create("div", "live-chart-head");
  head.append(title, filters, minutesLabel);
  const rangeTools = create("div", "live-trend-range");
  rangeTools.append(presets, interval);
  chart.append(head, rangeTools, canvas, legend);
  number.onchange = () => setMinutes(number.valueAsNumber);
  number.onkeydown = (event) => { if (event.key === "Enter") setMinutes(number.valueAsNumber); };

  function selectedSeries() {
    return (data?.series ?? []).map((item, index) => ({ ...item, index, color: activityColors[index % activityColors.length] }))
      .filter((item) => selected.has(item.id));
  }

  function draw() {
    layout = drawActivityChart(canvas, data, selectedSeries(), hover);
  }

  function syncPreset() {
    number.value = String(minutes);
    for (const [value, button] of presetButtons) button.setAttribute("aria-pressed", String(value === minutes));
  }

  function setMinutes(value) {
    const next = Number.isFinite(value) ? Math.min(maxChartMinutes, Math.max(1, Math.trunc(value))) : minutes;
    if (next === minutes) { syncPreset(); return; }
    minutes = next;
    data = null;
    hover = null;
    canvas.title = "";
    interval.textContent = "加载中";
    syncPreset();
    draw();
    try { localStorage.setItem("link-chat-live-trend-minutes", String(minutes)); } catch { /* session only */ }
    onRangeChange();
  }

  const checks = new Map();
  function syncLegend() {
    const enabled = selectedSeries();
    filterSummary.textContent = `分类 ${enabled.length}`;
    const keys = enabled.slice(0, 6).map(({ label, color }) => {
      const item = create("span", "", label);
      item.style.setProperty("--series-color", color);
      return item;
    });
    if (data && !enabled.length) keys.push(create("small", "", "请选择分类"));
    if (enabled.length > keys.length) keys.push(create("small", "", `+${enabled.length - keys.length} 类`));
    legend.replaceChildren(...keys);
  }

  function applySelection() {
    for (const [id, input] of checks) input.checked = selected.has(id);
    try { localStorage.setItem("link-chat-live-trend-series", JSON.stringify([...selected])); } catch { /* session only */ }
    if (hover !== null && (!data || hover >= data.samples.length)) hover = null;
    showSample();
    syncLegend();
    draw();
  }

  function setData(next) {
    if (!next || next.minutes !== minutes) return;
    const replacementAt = next.samples[0]?.at;
    if (data && replacementAt > next.start_at) {
      const keep = data.samples.filter(({ at }) => at >= next.start_at && at < replacementAt);
      next = { ...next, samples: [...keep, ...next.samples] };
    }
    data = next;
    if (!checks.size) {
      for (const [index, item] of data.series.entries()) {
        const field = create("label", "", item.label);
        const input = create("input", "");
        input.type = "checkbox";
        input.checked = selected.has(item.id);
        input.onchange = () => {
          if (input.checked) selected.add(item.id);
          else selected.delete(item.id);
          applySelection();
        };
        field.style.setProperty("--series-color", activityColors[index % activityColors.length]);
        field.prepend(input);
        checklist.append(field);
        checks.set(item.id, input);
      }
    }
    if (hover !== null && hover >= data.samples.length) hover = null;
    showSample();
    interval.textContent = `每点 ${data.bucket_seconds} 秒`;
    canvas.setAttribute("aria-label", `最近 ${minutes} 分钟活动曲线，每点 ${data.bucket_seconds} 秒`);
    syncLegend();
    draw();
  }

  function showSample() {
    const sample = hover === null ? null : data?.samples[hover];
    if (!sample) { canvas.title = ""; return; }
    const time = new Date(sample.at).toLocaleTimeString([], { hour12: false });
    canvas.title = [time, ...selectedSeries().map(({ label, index }) => `${label} ${sample.counts[index]}`)].join("\n");
  }

  canvas.onpointermove = (event) => {
    if (!layout || !data) return;
    const fraction = (event.offsetX - layout.left) / (layout.right - layout.left);
    hover = Math.round(Math.max(0, Math.min(1, fraction)) * (data.samples.length - 1));
    showSample();
    draw();
  };
  canvas.onpointerleave = () => { hover = null; showSample(); draw(); };
  syncPreset();
  return { chart, canvas, draw, setData, minutes: () => minutes, ready: () => !!data,
    since: () => data?.samples.at(-1)?.at ?? 0 };
}

function renderLimit(label, key, initial, apply) {
  const input = create("input", "");
  input.type = "number";
  input.min = "0";
  input.max = String(maxSceneLimit);
  input.step = "1";
  let value = initial;
  try {
    const saved = localStorage.getItem(key);
    if (saved !== null) {
      const n = Number(saved);
      if (Number.isInteger(n) && n >= 0 && n <= maxSceneLimit) value = n;
    }
  } catch { /* The current view still works when browser storage is disabled. */ }
  input.value = String(value);
  input.addEventListener("input", () => {
    if (!Number.isFinite(input.valueAsNumber)) return;
    const next = Math.min(maxSceneLimit, Math.max(0, Math.trunc(input.valueAsNumber)));
    if (next === value) return;
    value = next;
    apply(value);
    try { localStorage.setItem(key, String(value)); } catch { /* session-only */ }
  });
  input.addEventListener("change", () => { input.value = String(value); });
  const field = create("label", "", label);
  field.append(input);
  return { field, value };
}

export function mountLive(view, api) {
  view.classList.add("live-view");
  const main = create("section", "live-space");
  main.setAttribute("aria-label", "实时用户星体与频道星环");
  const stage = create("div", "live-stage");
  const overlay = create("div", "live-overlay");
  const head = create("div", "live-space-head");
  const status = create("div", "live-status");
  status.setAttribute("role", "status");
  head.append(create("h2", "", "实况"), status);
  const legend = create("ul", "live-legend");
  for (const [text, type] of [["在线", "online"], ["离线", "offline"], ["频道", "channel"]]) {
    const item = create("li", "");
    item.append(create("i", type), document.createTextNode(text));
    legend.append(item);
  }
  const sceneCount = create("li", "");
  sceneCount.hidden = true;
  const messageKey = create("li", "live-length-key");
  messageKey.append(document.createTextNode("短消息"), create("span", "live-length-scale"), document.createTextNode("长消息"));
  legend.append(messageKey, sceneCount);
  const fallback = create("p", "live-fallback");
  fallback.hidden = true;
  const hud = create("header", "live-hud");
  const stats = create("dl", "live-stats");
  const values = new Map();
  for (const [key, caption] of [["online", "在线用户"], ["channels", "频道"], ["today", "今日消息"],
    ["minute", "近一分钟消息"], ["social", "近一分钟社交"], ["uptime", "运行时长"]]) {
    const group = create("div");
    const value = create("dd", "", "—");
    values.set(key, value);
    group.append(create("dt", "", caption), value);
    stats.append(group);
  }
  const feed = create("details", "live-feed");
  feed.open = true;
  const events = create("ol", "live-events");
  const empty = create("p", "live-empty", "等待活动…");
  feed.append(create("summary", "", "活动日志"), empty, events);
  const controls = create("details", "live-controls");
  const trendChart = activityChart(() => loadStats());
  const selection = create("section", "live-selection");
  const playerSelect = create("select", "");
  const channelSelect = create("select", "");
  const search = create("input", "");
  search.type = "search";
  search.placeholder = "搜索玩家或频道";
  search.setAttribute("aria-label", search.placeholder);
  playerSelect.setAttribute("aria-label", "选中玩家");
  channelSelect.setAttribute("aria-label", "选中频道");
  const details = create("div", "live-details");
  details.hidden = true;
  selection.append(search, playerSelect, channelSelect, details);
  const limits = create("fieldset", "live-limits");
  const usersControl = renderLimit("用户上限", "link-chat-live-user-limit", defaultUsersLimit, (value) => {
    usersLimit = value;
    syncScene();
  });
  const channelsControl = renderLimit("频道上限", "link-chat-live-channel-limit", defaultChannelsLimit, (value) => {
    channelsLimit = value;
    channelRefillAt = 0;
    syncScene();
  });
  let usersLimit = usersControl.value, channelsLimit = channelsControl.value;
  limits.append(create("legend", "", "3D 显示上限"), usersControl.field, channelsControl.field);
  controls.append(create("summary", "", "场景控制"), limits, selection);
  const dock = create("aside", "live-dock");
  dock.append(controls, trendChart.chart, feed);
  hud.append(head, stats);
  main.append(stage, overlay, hud, dock, legend, fallback);
  view.replaceChildren(main);

  let scene;
  try {
    scene = new LiveScene(stage, overlay, choose);
  } catch {
    fallback.textContent = "此浏览器无法启动 3D 场景";
    fallback.hidden = false;
  }

  let users = new Map();
  let channels = new Map();
  let membership = new Map();
  let recentUsers = new Map();
  let recentChannels = new Map();
  let displayedChannels = new Map();
  const channelDepartures = new Map();
  let onlineCount = 0;
  let statsData;
  let sequence = 0;
  let focusSequence = 0;
  let chatDetails = false;
  let buffered = [];
  let bufferOverflow = false;
  let loading = false;
  let connected = api.connected();
  let closed = false;
  let request = 0;
  let statsPending = false;
  let sceneFrame;
  let logFrame;
  let sceneTimer;
  let channelRefillTimer;
  let channelRefillAt = 0;
  let snapshotTimer;
  let selected = null;
  let friendIDs = [];
  let friendRequest = 0;
  let animations = [];
  let logs = [];
  const displaySize = typeof ResizeObserver === "function" ? new ResizeObserver(() => {
    main.style.setProperty("--live-stage-top", `${Math.ceil(hud.offsetTop + hud.offsetHeight + 8)}px`);
    trendChart.draw();
  }) : null;
  displaySize?.observe(hud);
  displaySize?.observe(trendChart.canvas);

  playerSelect.onchange = () => choose("user", playerSelect.value);
  channelSelect.onchange = () => choose("channel", channelSelect.value);
  search.addEventListener("input", syncScene);

  function choose(kind, id) {
    selected = id ? { kind, id } : null;
    if (selected) controls.open = true;
    friendIDs = [];
    refreshFriends();
    syncScene();
  }

  async function refreshFriends() {
    const generation = ++friendRequest;
    if (!chatDetails || selected?.kind !== "user") return;
    try {
      const res = await api.fetchFriends(selected.id);
      if (!closed && generation === friendRequest) {
        friendIDs = res.friends;
        syncScene();
      }
    } catch {
      if (generation === friendRequest) {
        friendIDs = [];
        syncScene();
      }
    }
  }

  function updateOptions(select, title, options, value) {
    const key = JSON.stringify(options);
    if (select.dataset.options !== key) {
      select.dataset.options = key;
      select.replaceChildren(new Option(title, ""), ...options.map(([id, label]) => new Option(label, id)));
    }
    select.value = value || "";
  }

  function searchOptions(source, label, picked) {
    const query = search.value.trim().toLowerCase();
    const options = [];
    const chosen = source.get(picked);
    if (chosen) options.push([picked, label(chosen)]);
    for (const item of source.values()) {
      const text = label(item);
      if (item.id !== picked && text.toLowerCase().includes(query)) options.push([item.id, text]);
      if (options.length >= optionsLimit) break;
    }
    return options;
  }

  function drawSelection(visibleUsers, visibleChannels) {
    const pickedUser = selected?.kind === "user" ? selected.id : "";
    const pickedChannel = selected?.kind === "channel" ? selected.id : "";
    updateOptions(playerSelect, "选择玩家", searchOptions(users, (u) => u.name, pickedUser), pickedUser);
    updateOptions(channelSelect, "选择频道", searchOptions(channels, (ch) => ch.label, pickedChannel), pickedChannel);

    let title = "", roster = [], rings = [];
    if (selected?.kind === "channel") {
      const channel = channels.get(selected.id);
      if (channel) {
        title = `${channel.label} · 在线 ${channel.online_count}/${channel.member_count}`;
        roster = chatDetails ? channel.members : [];
      }
    } else if (selected?.kind === "user") {
      const user = users.get(selected.id);
      title = user?.name ?? "";
      if (chatDetails) {
        rings = [...(membership.get(selected.id) ?? [])];
        roster = friendIDs;
      }
    }
    scene?.select(selected, rings.filter((id) => visibleChannels.has(id)),
      chatDetails ? friendIDs.filter((id) => visibleUsers.has(id)) : []);
    const online = [], offline = [];
    let totalPeople = 0;
    for (const id of roster) {
      const person = users.get(id);
      if (!person) continue;
      totalPeople++;
      const group = person.online ? online : offline;
      if (group.length < optionsLimit) group.push(person);
    }
    const shown = online.concat(offline.slice(0, optionsLimit - online.length));
    shown.sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
    const key = JSON.stringify([title, chatDetails, selected, rings.length, totalPeople, shown.map((u) => [u.name, u.online, u.viewing_channel === selected?.id])]);
    if (details.dataset.content === key) return;
    details.dataset.content = key;
    details.hidden = !title;
    details.replaceChildren(create("strong", "", title));
    if (selected?.kind === "user" && chatDetails) {
      details.append(create("p", "live-relations", `金线 · ${rings.length} 个频道　粉线 · ${totalPeople} 位好友`));
    }
    if (shown.length) {
      const list = create("ul", "live-people");
      for (const u of shown) {
        const state = u.viewing_channel === selected?.id ? "正在查看" : u.online ? "在线" : "离线";
        const item = create("li", "");
        item.append(create("span", "", u.name), create("span", u.online ? "online" : "", state));
        list.append(item);
      }
      details.append(list);
      if (shown.length < totalPeople) details.append(create("p", "live-relations", `另有 ${totalPeople - shown.length} 人`));
    }
  }

  function setStatus(text, online = false) {
    status.textContent = text;
    status.classList.toggle("connected", online);
  }

  function drawStats() {
    values.get("online").textContent = `${onlineCount} / ${users.size}`;
    values.get("channels").textContent = String(channels.size);
    if (statsData) {
      values.get("today").textContent = String(statsData.messages_today);
      values.get("minute").textContent = String(statsData.messages_minute);
      if (statsData.started_at) {
        const seconds = Math.max(0, Math.floor((Date.now() - statsData.started_at) / 1000));
        const hhmmss = [Math.floor(seconds / 3600), Math.floor(seconds / 60) % 60, seconds % 60];
        values.get("uptime").textContent = hhmmss.map((v) => String(v).padStart(2, "0")).join(":");
      }
    }
  }

  function applyStats(next) {
    statsData = next;
    if (next?.trend) {
      values.get("social").textContent = String(next.trend.reduce((sum, bucket) => sum + bucket.counts[1], 0));
    }
    drawStats();
    trendChart.setData(next?.activity_trend);
  }

  function syncScene() {
    if (sceneFrame !== undefined || sceneTimer !== undefined) return;
    if (users.size > defaultUsersLimit || channels.size > defaultChannelsLimit) {
      sceneTimer = setTimeout(() => { sceneTimer = undefined; sceneFrame = requestAnimationFrame(drawScene); }, 100);
    } else {
      sceneFrame = requestAnimationFrame(drawScene);
    }
  }

  function remember(recent, id, limit) {
    recent.delete(id);
    recent.set(id, true);
    while (recent.size > limit) recent.delete(recent.keys().next().value);
  }

  function visible(source, recent, pinned, limit) {
    if (limit === 0) return new Map();
    if (source.size <= limit) return source;
    const items = new Map();
    for (const ids of [pinned, Array.from(recent.keys()).reverse(), source.keys()]) {
      for (const id of ids) {
        if (source.has(id)) items.set(id, source.get(id));
        if (items.size >= limit) return items;
      }
    }
    return items;
  }

  function holdChannelRefill() {
    if (!displayedChannels.size) return;
    channelRefillAt = performance.now() + channelRefillDelay;
    if (channelRefillTimer !== undefined) return;
    function resume() {
      const left = channelRefillAt - performance.now();
      if (left > 0) {
        channelRefillTimer = setTimeout(resume, left);
      } else {
        channelRefillTimer = undefined;
        syncScene();
      }
    }
    channelRefillTimer = setTimeout(resume, channelRefillDelay);
  }

  function pickVisibleChannels(pickedChannel, pickedUser) {
    if (channelsLimit === 0) return new Map();
    const pinned = [pickedChannel, users.get(pickedUser)?.viewing_channel];
    if (performance.now() < channelRefillAt) {
      const stable = new Map();
      for (const id of [...pinned, ...displayedChannels.keys()]) {
        if (channels.has(id)) stable.set(id, channels.get(id));
        if (stable.size === channelsLimit) break;
      }
      return stable;
    }
    return visible(channels, recentChannels, [...pinned, ...(membership.get(pickedUser) ?? [])], channelsLimit);
  }

  function drawScene() {
    sceneFrame = undefined;
    const pickedUser = selected?.kind === "user" ? selected.id : "";
    const pickedChannel = selected?.kind === "channel" ? selected.id : "";
    const visibleUsers = visible(users, recentUsers, [pickedUser, ...friendIDs], usersLimit);
    const visibleChannels = pickVisibleChannels(pickedChannel, pickedUser);
    for (const ch of visibleChannels.values()) {
      if (chatDetails) {
        ch.member_count = ch.members.length;
        ch.online_count = ch.members.filter((id) => users.get(id)?.online).length;
      }
    }
    sceneCount.hidden = visibleUsers.size === users.size && visibleChannels.size === channels.size;
    sceneCount.textContent = `场景显示 ${visibleUsers.size}/${users.size} 用户 · ${visibleChannels.size}/${channels.size} 频道`;
    for (const [id, name] of channelDepartures) scene?.departChannel(id, name);
    channelDepartures.clear();
    const departingUsers = animations.filter((a) => a.kind === "account.deleted" && scene?.users.has(a.actor.id));
    for (const activity of departingUsers.slice(-8)) scene?.departUser(activity.actor.id);
    scene?.sync(visibleUsers, visibleChannels);
    displayedChannels = new Map(visibleChannels);
    drawSelection(visibleUsers, visibleChannels);
    drawStats();
    for (const activity of animations) scene?.enqueue(activity);
    animations = [];
  }

  async function loadStats() {
    if (closed || !connected || loading || statsPending || document.hidden) return;
    statsPending = true;
    const generation = request;
    const minutes = trendChart.minutes();
    try {
      const next = await api.fetchStats(minutes, trendChart.since(), statsData?.started_at ?? 0);
      if (!closed && generation === request) {
        applyStats(next);
      }
    } catch {
      // Snapshot loading handles connection and permission status.
    } finally {
      statsPending = false;
      if (minutes !== trendChart.minutes()) loadStats();
    }
  }

  function updateState(a) {
    sequence = a.sequence;
    if (a.kind === "account.deleted") {
      holdChannelRefill();
      if (users.get(a.actor.id)?.online) onlineCount--;
      users.delete(a.actor.id);
      recentUsers.delete(a.actor.id);
      for (const id of membership.get(a.actor.id) ?? []) {
        const ch = channels.get(id);
        if (ch) ch.members = ch.members.filter((id) => id !== a.actor.id);
      }
      membership.delete(a.actor.id);
      if (selected?.kind === "user" && selected.id === a.actor.id) {
        selected = null;
        friendIDs = [];
        friendRequest++;
      }
      return;
    }
    if (a.actor.id && a.actor.id !== "cli") {
      const actor = users.get(a.actor.id);
      let online = actor?.online ?? false;
      if (a.kind === "online") online = true;
      if (a.kind === "offline") online = false;
      onlineCount += Number(online) - Number(actor?.online ?? false);
      remember(recentUsers, a.actor.id, Math.max(defaultUsersLimit, usersLimit));
      users.set(a.actor.id, {
        ...actor, ...a.actor, online, last_active: a.at, last_sequence: a.sequence,
        viewing_channel: online ? actor?.viewing_channel : "",
      });
    }
    if (!a.channel) return;
    const { id, name } = a.channel;
    if (a.kind === "channel.deleted") {
      holdChannelRefill();
      for (const uid of channels.get(id)?.members ?? []) membership.get(uid)?.delete(id);
      channels.delete(id);
      recentChannels.delete(id);
      return;
    }
    const channel = channels.get(id) ?? { id, label: `频道 ${channels.size + 1}`, members: [], member_count: 0, online_count: 0 };
    remember(recentChannels, id, Math.max(defaultChannelsLimit, channelsLimit));
    if (name) {
      channel.name = name;
      channel.label = "#" + name;
      if (a.kind === "channel.created" || a.kind === "channel.joined") {
        addMembership(a.actor.id, id);
        if (!channel.members.includes(a.actor.id)) channel.members.push(a.actor.id);
      }
      if (a.kind === "channel.left") {
        membership.get(a.actor.id)?.delete(id);
        channel.members = channel.members.filter((uid) => uid !== a.actor.id);
      }
    }
    channels.set(id, channel);
  }

  function addMembership(uid, id) {
    if (!membership.has(uid)) membership.set(uid, new Set());
    membership.get(uid).add(id);
  }

  function refreshSnapshotAfter(delay) {
    if (snapshotTimer !== undefined) return;
    snapshotTimer = setTimeout(() => {
      snapshotTimer = undefined;
      if (connected && !loading) loadSnapshot();
    }, delay);
  }

  function receive(a) {
    if (a.kind === "channel.deleted" && a.channel && displayedChannels.has(a.channel.id)) {
      channelDepartures.set(a.channel.id, a.channel.name);
    }
    if (a.sequence > sequence) {
      updateState(a);
      if ((a.kind === "moment.liked" || a.kind === "moment.commented") && users.has(a.target?.id)) {
        remember(recentUsers, a.target.id, Math.max(defaultUsersLimit, usersLimit));
      }
      if (!chatDetails && (a.kind === "online" || a.kind === "offline" || a.kind === "account.deleted" || a.kind.startsWith("channel."))) {
        refreshSnapshotAfter(1500);
      }
      if (a.kind.startsWith("friend.") && [a.actor.id, a.target?.id].includes(selected?.id)) refreshFriends();
    }
    if (a.kind === "message" && scene?.hasMessageEndpoints(a)) {
      scene.enqueue(a);
    } else {
      if (animations.length === 200) animations.shift();
      animations.push(a);
    }
    if (logs.length === 100) logs.shift();
    logs.push(a);
    if (logFrame === undefined) logFrame = requestAnimationFrame(flushLogs);
    syncScene();
  }

  function flushLogs() {
    logFrame = undefined;
    for (const activity of logs) appendLog(activity);
    logs = [];
  }

  function appendLog(a) {
    const when = new Date(a.at);
    const time = create("time", "", activityClock.format(when));
    time.dateTime = when.toISOString();
    if (a.kind === "account.deleted") {
      for (const row of Array.from(events.children)) {
        if (row.dataset.actor === a.actor.id || row.dataset.target === a.actor.id) row.remove();
      }
    }
    const channelDeleted = a.kind === "channel.deleted";
    const row = create("li", channelDeleted ? "channel-deleted" : a.kind === "account.deleted" ? "account-deleted" : "");
    row.dataset.actor = channelDeleted ? "" : a.actor.id;
    row.dataset.target = a.target?.id || "";
    const summary = create("span", "live-summary", channelDeleted ? `频道已销毁${a.channel?.name ? " · #" + a.channel.name : ""}` : a.summary);
    if (a.kind === "moment.commented" && a.content) summary.append(create("q", "live-comment", a.content));
    row.append(time, create("span", `live-kind ${categories.get(a.category) ?? "system"}`, a.category), summary);
    events.prepend(row);
    empty.hidden = true;
    if (events.children.length > 100) events.lastElementChild.remove();
  }

  function focus(a) {
    if (!chatDetails || a.sequence <= focusSequence) return;
    focusSequence = a.sequence;
    const user = users.get(a.user_id);
    if (user) user.viewing_channel = a.channel_id;
    if (a.channel_id) remember(recentChannels, a.channel_id, Math.max(defaultChannelsLimit, channelsLimit));
    syncScene();
  }

  async function loadSnapshot() {
    const generation = ++request;
    loading = true;
    try {
      const snap = await api.fetchSnapshot();
      if (closed || generation !== request) return;
      users = new Map(snap.users.map((u) => [u.id, u]));
      onlineCount = snap.users.reduce((count, user) => count + Number(user.online ?? false), 0);
      channels = new Map(snap.channels.map((ch, i) => [ch.id, {
        ...ch, label: ch.name ? "#" + ch.name : `频道 ${i + 1}`,
        members: ch.members ?? [], member_count: ch.member_count ?? 0, online_count: ch.online_count ?? 0,
      }]));
      membership = new Map();
      for (const ch of channels.values()) for (const uid of ch.members) addMembership(uid, ch.id);
      recentUsers = new Map(snap.users.slice().sort((a, b) => (b.last_sequence ?? 0) - (a.last_sequence ?? 0))
        .slice(0, Math.max(defaultUsersLimit, usersLimit)).reverse().map((u) => [u.id, true]));
      recentChannels = new Map([...recentChannels].filter(([id]) => channels.has(id)));
      applyStats(snap.stats);
      chatDetails = !!snap.chat_details;
      sequence = snap.sequence;
      focusSequence = snap.focus_sequence ?? 0;
      for (const event of buffered) (event.type === "activity" ? receive : focus)(event.data);
      buffered = [];
      if (bufferOverflow) refreshSnapshotAfter(300);
      bufferOverflow = false;
      refreshFriends();
      drawStats();
      syncScene();
      setStatus(connected ? "实时连接" : "连接中", connected);
    } catch (error) {
      if (!closed && generation === request) {
        setStatus(api.errText(error));
        buffered = [];
        bufferOverflow = false;
      }
    } finally {
      if (generation === request) {
        loading = false;
        if (!trendChart.ready()) loadStats();
      }
    }
  }

  api.onEvent((event) => {
    switch (event.type) {
      case "stream.open":
        connected = true;
        loadSnapshot();
        break;
      case "stream.closed":
        connected = false;
        ++request;
        loading = false;
        buffered = [];
        bufferOverflow = false;
        chatDetails = false;
        ++friendRequest;
        friendIDs = [];
        animations = [];
        channelDepartures.clear();
        logs = [];
        membership = new Map();
        scene?.clearEffects();
        for (const user of users.values()) delete user.viewing_channel;
        let index = 1;
        for (const channel of channels.values()) {
          delete channel.name;
          channel.label = `频道 ${index++}`;
          channel.members = [];
        }
        syncScene();
        events.replaceChildren();
        empty.hidden = false;
        setStatus("重新连接中");
        break;
      case "activity":
      case "channel.focus":
        if (loading) {
          if (buffered.length === 512) {
            buffered.shift();
            bufferOverflow = true;
          }
          buffered.push(event);
        } else {
          (event.type === "activity" ? receive : focus)(event.data);
        }
        break;
    }
  });
  setStatus("正在连接…");
  loadSnapshot();
  const statsInterval = setInterval(loadStats, 1000);

  return () => {
    closed = true;
    displaySize?.disconnect();
    clearInterval(statsInterval);
    clearTimeout(snapshotTimer);
    clearTimeout(sceneTimer);
    clearTimeout(channelRefillTimer);
    cancelAnimationFrame(sceneFrame);
    cancelAnimationFrame(logFrame);
    scene?.dispose();
  };
}
