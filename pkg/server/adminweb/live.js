import { LiveScene } from "./live-scene.js";

const categories = new Map([["消息", "message"], ["社交", "social"], ["账号", "account"], ["系统", "system"]]);

function create(tag, className, text = "") {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text) node.textContent = text;
  return node;
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
  const fallback = create("p", "live-fallback");
  fallback.hidden = true;
  main.append(stage, overlay, head, legend, fallback);

  const sidebar = create("aside", "live-sidebar");
  const stats = create("dl", "live-stats");
  const values = new Map();
  for (const [key, caption] of [["online", "在线用户"], ["today", "今日消息"], ["minute", "近一分钟"], ["channels", "频道"]]) {
    const group = create("div");
    const value = create("dd", "", "—");
    values.set(key, value);
    group.append(create("dt", "", caption), value);
    stats.append(group);
  }
  const feed = create("section", "live-feed");
  const events = create("ol", "live-events");
  const empty = create("p", "live-empty", "等待活动…");
  feed.append(create("h3", "", "活动日志"), empty, events);
  const selection = create("section", "live-selection");
  const playerSelect = create("select", "");
  const channelSelect = create("select", "");
  playerSelect.setAttribute("aria-label", "选中玩家");
  channelSelect.setAttribute("aria-label", "选中频道");
  const details = create("div", "live-details");
  details.hidden = true;
  selection.append(playerSelect, channelSelect, details);
  sidebar.append(create("h3", "", "实时统计"), stats, selection, feed);
  view.replaceChildren(main, sidebar);

  let scene;
  try {
    scene = new LiveScene(stage, overlay, choose);
  } catch {
    fallback.textContent = "此浏览器无法启动 3D 场景";
    fallback.hidden = false;
  }

  let users = new Map();
  let channels = new Map();
  let statsData;
  let sequence = 0;
  let focusSequence = 0;
  let chatDetails = false;
  let buffered = [];
  let loading = false;
  let connected = api.connected();
  let closed = false;
  let request = 0;
  let statsTimer;
  let statsPending = false;
  let sceneFrame;
  let snapshotTimer;
  let selected = null;
  let friendIDs = [];
  let friendRequest = 0;
  let animations = [];

  playerSelect.onchange = () => choose("user", playerSelect.value);
  channelSelect.onchange = () => choose("channel", channelSelect.value);

  function choose(kind, id) {
    selected = id ? { kind, id } : null;
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

  function drawSelection() {
    updateOptions(playerSelect, "选择玩家", [...users.values()].sort((a, b) => a.name.localeCompare(b.name))
      .map((u) => [u.id, u.name]), selected?.kind === "user" && selected.id);
    updateOptions(channelSelect, "选择频道", [...channels.values()].map((ch) => [ch.id, ch.label]),
      selected?.kind === "channel" && selected.id);

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
        rings = [...channels.values()].filter((ch) => ch.members.includes(selected.id)).map((ch) => ch.id);
        roster = friendIDs;
      }
    }
    scene?.select(selected, rings, chatDetails ? friendIDs : []);
    const people = roster.map((id) => users.get(id)).filter(Boolean);
    people.sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
    const key = JSON.stringify([title, chatDetails, selected, rings.length, people.map((u) => [u.name, u.online, u.viewing_channel === selected?.id])]);
    if (details.dataset.content === key) return;
    details.dataset.content = key;
    details.hidden = !title;
    details.replaceChildren(create("strong", "", title));
    if (selected?.kind === "user" && chatDetails) {
      details.append(create("p", "live-relations", `金线 · ${rings.length} 个频道　粉线 · ${people.length} 位好友`));
    }
    if (people.length) {
      const list = create("ul", "live-people");
      for (const u of people.values()) {
        const state = u.viewing_channel === selected?.id ? "正在查看" : u.online ? "在线" : "离线";
        const item = create("li", "");
        item.append(create("span", "", u.name), create("span", u.online ? "online" : "", state));
        list.append(item);
      }
      details.append(list);
    }
  }

  function setStatus(text, online = false) {
    status.textContent = text;
    status.classList.toggle("connected", online);
  }

  function drawStats() {
    let online = 0;
    for (const user of users.values()) if (user.online) online++;
    values.get("online").textContent = `${online} / ${users.size}`;
    values.get("channels").textContent = String(channels.size);
    if (statsData) {
      values.get("today").textContent = String(statsData.messages_today);
      values.get("minute").textContent = String(statsData.messages_minute);
    }
  }

  function syncScene() {
    if (sceneFrame !== undefined) return;
    sceneFrame = requestAnimationFrame(() => {
      sceneFrame = undefined;
      let index = 1;
      for (const ch of channels.values()) {
        ch.label = ch.name ? "#" + ch.name : `频道 ${index}`;
        if (chatDetails) {
          ch.member_count = ch.members.length;
          ch.online_count = ch.members.filter((id) => users.get(id)?.online).length;
        }
        index++;
      }
      scene?.sync(users, channels);
      drawSelection();
      for (const activity of animations) scene?.enqueue(activity);
      animations = [];
    });
  }

  async function loadStats() {
    statsTimer = undefined;
    if (closed || !connected || loading || statsPending || document.hidden) return;
    statsPending = true;
    const generation = request;
    try {
      const next = await api.fetchStats();
      if (!closed && generation === request) {
        statsData = next;
        drawStats();
      }
    } catch {
      // Snapshot loading handles connection and permission status.
    } finally {
      statsPending = false;
    }
  }

  function updateState(a) {
    sequence = a.sequence;
    if (a.actor.id && a.actor.id !== "cli") {
      const actor = users.get(a.actor.id);
      let online = actor?.online ?? false;
      if (a.kind === "online") online = true;
      if (a.kind === "offline") online = false;
      users.set(a.actor.id, {
        ...actor, ...a.actor, online, last_active: a.at, last_sequence: a.sequence,
        viewing_channel: online ? actor?.viewing_channel : "",
      });
    }
    if (!a.channel) return;
    const { id, name } = a.channel;
    if (a.kind === "channel.deleted") {
      channels.delete(id);
      return;
    }
    const channel = channels.get(id) ?? { id, members: [], member_count: 0, online_count: 0 };
    if (name) {
      channel.name = name;
      const memberIDs = new Set(channel.members);
      if (a.kind === "channel.created" || a.kind === "channel.joined") memberIDs.add(a.actor.id);
      if (a.kind === "channel.left") memberIDs.delete(a.actor.id);
      channel.members = Array.from(memberIDs);
    }
    channels.set(id, channel);
  }

  function receive(a) {
    if (a.sequence > sequence) {
      updateState(a);
      if (!chatDetails && (a.kind === "online" || a.kind === "offline" || a.kind.startsWith("channel."))) {
        if (snapshotTimer === undefined) snapshotTimer = setTimeout(() => {
          snapshotTimer = undefined;
          if (connected && !loading) loadSnapshot();
        }, 350);
      }
      if (a.kind.startsWith("friend.") && [a.actor.id, a.target?.id].includes(selected?.id)) refreshFriends();
    }
    if (animations.length === 200) animations.shift();
    animations.push(a);
    syncScene();
    drawStats();
    if (a.kind === "message" && statsTimer === undefined) statsTimer = setTimeout(loadStats, 250);
    const when = new Date(a.at);
    const time = create("time", "", when.toLocaleTimeString("zh-CN", { hour12: false }));
    time.dateTime = when.toISOString();
    const row = create("li", "");
    row.append(time, create("span", `live-kind ${categories.get(a.category) ?? "system"}`, a.category),
      create("span", "live-summary", a.summary));
    events.prepend(row);
    empty.hidden = true;
    if (events.children.length > 100) events.lastElementChild.remove();
  }

  function focus(a) {
    if (!chatDetails || a.sequence <= focusSequence) return;
    focusSequence = a.sequence;
    const user = users.get(a.user_id);
    if (user) user.viewing_channel = a.channel_id;
    syncScene();
  }

  async function loadSnapshot() {
    const generation = ++request;
    loading = true;
    try {
      const snap = await api.fetchSnapshot();
      if (closed || generation !== request) return;
      users = new Map(snap.users.map((u) => [u.id, u]));
      channels = new Map(snap.channels.map((ch) => [ch.id, {
        ...ch, members: ch.members ?? [], member_count: ch.member_count ?? 0, online_count: ch.online_count ?? 0,
      }]));
      statsData = snap.stats;
      chatDetails = !!snap.chat_details;
      sequence = snap.sequence;
      focusSequence = snap.focus_sequence ?? 0;
      for (const event of buffered) (event.type === "activity" ? receive : focus)(event.data);
      buffered = [];
      refreshFriends();
      drawStats();
      syncScene();
      setStatus(connected ? "实时连接" : "连接中", connected);
    } catch (error) {
      if (!closed && generation === request) {
        setStatus(api.errText(error));
        buffered = [];
      }
    } finally {
      if (generation === request) loading = false;
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
        chatDetails = false;
        ++friendRequest;
        friendIDs = [];
        animations = [];
        scene?.clearEffects();
        for (const user of users.values()) delete user.viewing_channel;
        for (const channel of channels.values()) {
          delete channel.name;
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
          if (buffered.length === 256) buffered.shift();
          buffered.push(event);
        } else {
          (event.type === "activity" ? receive : focus)(event.data);
        }
        break;
    }
  });
  setStatus("正在连接…");
  loadSnapshot();
  const statsInterval = setInterval(() => {
    if (statsTimer === undefined) loadStats();
  }, 5000);

  return () => {
    closed = true;
    clearInterval(statsInterval);
    clearTimeout(statsTimer);
    clearTimeout(snapshotTimer);
    cancelAnimationFrame(sceneFrame);
    scene?.dispose();
  };
}
