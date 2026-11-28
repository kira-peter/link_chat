import * as THREE from "./vendor/three.module.min.js";
import { EffectComposer } from "./vendor/postprocessing/EffectComposer.js";
import { RenderPass } from "./vendor/postprocessing/RenderPass.js";
import { UnrealBloomPass } from "./vendor/postprocessing/UnrealBloomPass.js";
import { OutputPass } from "./vendor/postprocessing/OutputPass.js";

const GOLDEN_ANGLE = Math.PI * (3 - Math.sqrt(5));
const CYAN = 0x8adfff;
const GOLD = 0xffb968;
const PINK = 0xff85ce;
const RED = 0xff646e;
const MESSAGE_COLORS = [CYAN, 0x669dff, 0x55e2eb, 0xffdf71, 0xffac52, 0xff706c];
const CHANNEL_DEPARTURE_MS = 1350;
const MAX_EFFECTS = 200;
const pendingEffectKinds = new Set(["message", "online", "friend.accepted", "moment.liked", "moment.commented",
  "sanction.ban", "sanction.mute", "sanction.revoked", "sanction.expired.ban", "sanction.expired.mute"]);
const USER_BATCH_THRESHOLD = 500, CHANNEL_BATCH_THRESHOLD = 120;

function glowTexture() {
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 64;
  const ctx = canvas.getContext("2d");
  const gradient = ctx.createRadialGradient(32, 32, 0, 32, 32, 32);
  gradient.addColorStop(0, "#fff");
  gradient.addColorStop(0.23, "#ffffff80");
  gradient.addColorStop(0.52, "#ffffff28");
  gradient.addColorStop(1, "#ffffff00");
  ctx.fillStyle = gradient;
  ctx.fillRect(0, 0, 64, 64);
  return new THREE.CanvasTexture(canvas);
}

function background(glow) {
  const positions = [], colors = [];
  const tint = new THREE.Color();
  let seed = 7123;
  const random = () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
    return seed / 4294967296;
  };
  for (let i = 0; i < 1800; i++) {
    positions.push((random() - 0.5) * 250, (random() - 0.5) * 165, -25 - random() * 95);
    tint.setHSL(0.53 + random() * 0.17, 0.22 + random() * 0.55, 0.24 + random() * 0.45);
    colors.push(tint.r, tint.g, tint.b);
  }
  const geometry = new THREE.BufferGeometry();
  geometry.setAttribute("position", new THREE.Float32BufferAttribute(positions, 3));
  geometry.setAttribute("color", new THREE.Float32BufferAttribute(colors, 3));
  const material = new THREE.PointsMaterial({
    size: 0.52, map: glow, vertexColors: true, transparent: true, depthWrite: false,
    blending: THREE.AdditiveBlending,
  });
  return new THREE.Points(geometry, material);
}

export class LiveScene {
  constructor(stage, overlay, onSelect) {
    this.stage = stage;
    this.overlay = overlay;
    this.motion = matchMedia("(prefers-reduced-motion: reduce)");
    this.users = new Map();
    this.channels = new Map();
    this.departingChannels = new Set();
    this.instanceBatches = new Map();
    this.instanceTransform = new THREE.Object3D();
    this.pending = [];
    this.channelMessages = [];
    this.effects = [];
    this.relationships = [];
    this.selectionKey = "";
    this.ray = new THREE.Raycaster();
    this.projected = new THREE.Vector3();
    this.pointer = null;
    this.renderer = new THREE.WebGLRenderer({ alpha: false, antialias: true, powerPreference: "low-power" });
    this.renderer.setPixelRatio(Math.min(devicePixelRatio, 1.5));
    this.renderer.domElement.setAttribute("aria-hidden", "true");
    this.scene = new THREE.Scene();
    this.scene.background = new THREE.Color(0x060a15);
    this.camera = new THREE.PerspectiveCamera(43, 1, 0.3, 400);
    this.composer = new EffectComposer(this.renderer);
    this.renderPass = new RenderPass(this.scene, this.camera);
    this.bloom = new UnrealBloomPass(new THREE.Vector2(1, 1), 0.65, 0.45, 0.65);
    this.outputPass = new OutputPass();
    this.composer.addPass(this.renderPass);
    this.composer.addPass(this.bloom);
    this.composer.addPass(this.outputPass);

    this.glow = glowTexture();
    this.sky = background(this.glow);
    this.scene.add(this.sky);
    this.sphere = new THREE.SphereGeometry(1, 12, 10);
    this.torus = new THREE.TorusGeometry(1, 0.028, 8, 64);
    this.onPointer = (event) => {
      const rect = stage.getBoundingClientRect();
      this.pointer = { x: event.clientX - rect.left, y: event.clientY - rect.top };
    };
    this.leavePointer = () => { this.pointer = null; };
    this.pickNode = (event) => {
      const rect = stage.getBoundingClientRect();
      const x = event.clientX - rect.left, y = event.clientY - rect.top;
      let nearest = 15, player = "";
      for (const [id, node] of this.users) {
        const p = this.projected.copy(node.object.position).project(this.camera);
        const d = Math.hypot(x - (p.x + 1) * this.width / 2, y - (1 - p.y) * this.height / 2);
        if (d < nearest) {
          player = id;
          nearest = d;
        }
      }
      if (player) {
        onSelect("user", player);
        return;
      }
      this.ray.setFromCamera(new THREE.Vector2(2 * x / this.width - 1, 1 - 2 * y / this.height), this.camera);
      const cores = [...this.channels.values()].map((node) => node.core);
      const hit = this.ray.intersectObjects(cores, false)[0];
      onSelect("channel", hit?.object.userData.channelID ?? "");
    };
    this.motionChanged = () => this.clearEffects();
    stage.append(this.renderer.domElement);
    stage.addEventListener("pointermove", this.onPointer);
    stage.addEventListener("pointerleave", this.leavePointer);
    stage.addEventListener("click", this.pickNode);
    this.motion.addEventListener("change", this.motionChanged);
    this.resizeObserver = new ResizeObserver(() => this.resize());
    this.resizeObserver.observe(stage);
    this.resize();
    this.frame = requestAnimationFrame((t) => this.draw(t));
  }

  resize() {
    this.width = Math.max(1, this.stage.clientWidth);
    this.height = Math.max(1, this.stage.clientHeight);
    this.camera.aspect = this.width / this.height;
    this.camera.updateProjectionMatrix();
    this.renderer.setSize(this.width, this.height, false);
    this.composer.setSize(this.width, this.height);
    for (const node of this.channels.values()) this.placeChannel(node);
  }

  halo(color) {
    return new THREE.Sprite(new THREE.SpriteMaterial({
      map: this.glow, color, transparent: true, depthWrite: false,
      blending: THREE.AdditiveBlending,
    }));
  }

  makeNode(isChannel) {
    const object = new THREE.Group();
    const core = new THREE.Mesh(isChannel ? this.torus : this.sphere, new THREE.MeshBasicMaterial());
    const halo = this.halo(isChannel ? GOLD : CYAN);
    object.add(halo, core);
    if (isChannel) core.rotation.x = 0.5;
    const label = document.createElement("span");
    label.className = "live-label";
    label.hidden = true;
    this.overlay.append(label);
    this.scene.add(object);
    return { object, core, halo, label, goal: new THREE.Vector3(), home: new THREE.Vector3(), placed: false, flash: 0 };
  }

  removeNode(node) {
    this.scene.remove(node.object);
    node.core.material.dispose();
    node.halo.material.dispose();
    node.label.remove();
  }

  departUser(id) {
    const node = this.users.get(id);
    if (!node || this.motion.matches) return;
    const at = performance.now();
    this.burst(node.object.position, RED, at);
    this.ripple(node, RED, at);
  }

  placeChannel(node) {
    const band = node.slot % 3;
    const angle = node.slot * GOLDEN_ANGLE + 0.55;
    const overflow = node.slot >= 48;
    const depth = overflow ? 7 + (node.slot % 9) * 2 : 5 + band * 4;
    const spread = 0.49 + (node.slot * 0.754877666 % 1) * 0.35;
    const halfHeight = Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2)) * (this.cameraDistance() + depth);
    node.goal.set(Math.cos(angle) * halfHeight * this.camera.aspect * (overflow ? spread : 0.5 + band * 0.155),
      Math.sin(angle) * halfHeight * (overflow ? spread * 0.9 : 0.46 + band * 0.15), -depth);
  }

  cameraDistance() {
    return Math.max(66, 91 / this.camera.aspect);
  }

  departChannel(id, name) {
    const node = this.channels.get(id);
    if (!node || this.motion.matches) return;
    this.channels.delete(id);
    node.departedAt = performance.now();
    node.core.visible = true;
    node.halo.visible = true;
    node.core.material.transparent = true;
    node.core.material.depthWrite = false;
    node.label.className = "live-label departing";
    node.label.textContent = name ? `#${name} · 频道销毁` : "频道销毁";
    this.departingChannels.add(node);
  }

  sync(users, channels) {
    for (const [id, node] of this.channels) {
      if (!channels.has(id)) {
        this.removeNode(node);
        this.channels.delete(id);
      }
    }
    for (const [id, node] of this.users) {
      if (!users.has(id)) {
        this.removeNode(node);
        this.users.delete(id);
      }
    }
    const occupied = new Set([...this.channels.values(), ...this.departingChannels].map((node) => node.slot));
    let availableSlot = 0;
    for (const ch of channels.values()) {
      let node = this.channels.get(ch.id);
      const size = 1.12 + Math.min(1.65, Math.log2(Math.max(1, ch.member_count)) * 0.4);
      if (!node) {
        node = this.makeNode(true);
        this.channels.set(ch.id, node);
        node.core.userData.channelID = ch.id;
        while (occupied.has(availableSlot)) availableSlot++;
        node.slot = availableSlot++;
        node.size = size;
        this.placeChannel(node);
        node.object.position.copy(node.goal);
      }
      node.targetSize = size;
      const label = `${ch.label} · ${ch.online_count}/${ch.member_count}`;
      if (node.label.textContent !== label) node.label.textContent = label;
      node.showLabel = channels.size <= 10;
    }
    const ranked = [...users.values()].sort((a, b) => b.last_active - a.last_active
      || (b.last_sequence ?? 0) - (a.last_sequence ?? 0) || a.id.localeCompare(b.id));
    ranked.forEach((user, i) => {
      let node = this.users.get(user.id);
      if (!node) {
        node = this.makeNode(false);
        this.users.set(user.id, node);
      }
      const rank = Math.sqrt(i / Math.max(1, ranked.length - 1));
      const angle = i * GOLDEN_ANGLE + 2;
      const radius = 2.5 + rank * 8.5;
      node.home.set(Math.cos(angle) * radius, Math.sin(angle) * radius * 0.66, 4 - rank * 7);
      node.viewing = user.online ? user.viewing_channel : "";
      node.online = user.online;
      node.energy = 1 - rank * 0.42;
      node.size = Math.max(0.13, (user.online ? 0.47 : 0.30) * Math.min(1, 18 / Math.sqrt(ranked.length)));
      node.label.textContent = user.name;
      node.showLabel = ranked.length <= 6 || (user.online && i < 4);
      if (!node.placed) {
        node.phase = angle;
        node.object.position.copy(node.home);
        node.placed = true;
      }
    });
  }

  select(selected, channelIDs, friendIDs) {
    const key = JSON.stringify([selected, channelIDs, friendIDs]);
    if (key === this.selectionKey) return;
    this.selectionKey = key;
    this.selected = selected;
    this.selectedChannels = new Set(channelIDs);
    for (const edge of this.relationships) {
      this.scene.remove(edge.line);
      edge.line.geometry.dispose();
      edge.line.material.dispose();
    }
    this.relationships = [];
    if (selected?.kind !== "user") return;
    for (const [targets, color, channel] of [[channelIDs, GOLD, true], [friendIDs, PINK, false]]) {
      for (const id of targets) {
        const geometry = new THREE.BufferGeometry();
        geometry.setAttribute("position", new THREE.BufferAttribute(new Float32Array(6), 3));
        const line = new THREE.Line(geometry, new THREE.LineBasicMaterial({ color, transparent: true, opacity: 0.75 }));
        line.frustumCulled = false;
        this.relationships.push({ id, channel, line });
        this.scene.add(line);
      }
    }
  }

  updateRelationships() {
    const from = this.users.get(this.selected?.id)?.object.position;
    for (const edge of this.relationships) {
      const to = (edge.channel ? this.channels : this.users).get(edge.id)?.object.position;
      edge.line.visible = !!(from && to);
      if (!edge.line.visible) continue;
      const positions = edge.line.geometry.attributes.position;
      positions.setXYZ(0, from.x, from.y, from.z);
      positions.setXYZ(1, to.x, to.y, to.z);
      positions.needsUpdate = true;
    }
  }

  enqueue(a) {
    if (!pendingEffectKinds.has(a.kind)) return;
    if (this.pending.length === MAX_EFFECTS) this.pending.shift();
    this.pending.push({ activity: a, queuedAt: performance.now() });
  }

  hasMessageEndpoints(a) {
    return this.users.has(a.actor.id) && (a.channel ? this.channels.has(a.channel.id) : this.users.has(a.target?.id));
  }

  playPending(now) {
    while (this.pending.length && now - this.pending[0].queuedAt > 650) this.pending.shift();
    // Keep bursts moving over several frames without replaying an old backlog.
    const budget = Math.min(4, Math.ceil(this.pending.length / 8));
    for (let i = 0; i < budget; i++) this.play(this.pending.shift().activity, now);
  }

  clearEffects() {
    this.pending.length = 0;
    this.channelMessages.length = 0;
    for (const effect of this.effects) effect.dispose();
    this.effects.length = 0;
    for (const node of this.departingChannels) this.removeNode(node);
    this.departingChannels.clear();
  }

  addEffect(effect) {
    if (this.effects.length >= MAX_EFFECTS) this.effects.shift().dispose();
    this.effects.push(effect);
  }

  placeLabel(label, world, offsetX = 0, offsetY = 0) {
    const projected = this.projected.copy(world).project(this.camera);
    const x = (projected.x + 1) * this.width / 2;
    const y = (1 - projected.y) * this.height / 2;
    label.hidden = Math.abs(projected.x) > 1.02 || Math.abs(projected.y) > 1.02 || Math.abs(projected.z) > 1;
    label.style.transform = `translate(${Math.round(x + offsetX)}px, ${Math.round(y + offsetY)}px)`;
  }

  bubble(text, social = false) {
    const label = document.createElement("span");
    label.className = social ? "live-bubble social" : "live-bubble";
    label.textContent = text;
    this.overlay.append(label);
    return label;
  }

  play(a, now) {
    const from = this.users.get(a.actor.id);
    const target = a.target ? this.users.get(a.target.id) : undefined;
    switch (a.kind) {
      case "message": {
        const to = a.channel ? this.channels.get(a.channel.id) : target;
        if (!from || !to) return;
        const tint = MESSAGE_COLORS[a.message_band] ?? CYAN;
        if (a.channel && !this.motion.matches) {
          if (this.channelMessages.length === MAX_EFFECTS) this.channelMessages.shift();
          this.channelMessages.push({ from, to, channelID: a.channel.id, content: a.content, tint, queuedAt: now });
        } else {
          this.sendMeteor(from, to, a.content, now, tint);
        }
        break;
      }
      case "online":
        if (!this.motion.matches && from) {
          from.flash = now + 700;
          this.ripple(from, 0x7ef2c1, now);
        }
        break;
      case "friend.accepted":
        if (!this.motion.matches && from && target) this.link(from, target, now);
        break;
      case "moment.liked":
        if (target || from) this.heart(from ?? target, target ?? from, now);
        break;
      case "moment.commented":
        if (from || target) this.caption(from ?? target, a.content || "评论", now, true);
        break;
      case "sanction.ban":
      case "sanction.mute":
        if (!this.motion.matches && target) this.ripple(target, RED, now);
        break;
      case "sanction.revoked":
      case "sanction.expired.ban":
      case "sanction.expired.mute":
        if (!this.motion.matches && target) this.ripple(target, 0x7ef2c1, now);
        break;
    }
  }

  sendMeteor(from, to, content, now, tint) {
    if (this.motion.matches) {
      if (content) this.caption(to, content, now);
    } else {
      this.meteor(from, to, content, now, tint);
    }
  }

  meteor(from, to, content, start, tint) {
    const path = new THREE.QuadraticBezierCurve3();
    const tip = new THREE.Vector3();
    const head = this.halo(tint);
    head.scale.setScalar(2.3);
    const tailSize = 14;
    const trailGeometry = new THREE.BufferGeometry();
    const positions = new THREE.BufferAttribute(new Float32Array(tailSize * 3), 3);
    const colors = [];
    const color = new THREE.Color(tint);
    for (let i = 0; i < tailSize; i++) {
      const a = Math.pow(1 - i / tailSize, 2);
      colors.push(color.r * a, color.g * a, color.b * a);
    }
    trailGeometry.setAttribute("position", positions);
    trailGeometry.setAttribute("color", new THREE.Float32BufferAttribute(colors, 3));
    const tail = new THREE.Points(trailGeometry, new THREE.PointsMaterial({
      size: 0.94, map: this.glow, transparent: true, depthWrite: false,
      vertexColors: true, blending: THREE.AdditiveBlending,
    }));
    tail.frustumCulled = false;
    this.scene.add(tail, head);
    const origin = from.object.position.clone();
    const label = content ? this.bubble(content) : null;
    const updatePath = () => {
      path.v0.copy(origin);
      path.v2.copy(to.object.position);
      path.v1.copy(path.v0).lerp(path.v2, 0.5);
      path.v1.y += Math.min(8, 3 + path.v0.distanceTo(path.v2) * 0.22);
      path.v1.z += 5;
    };
    this.addEffect({
      tick: (now) => {
        const p = (now - start) / 1200;
        if (p >= 1) {
          head.visible = tail.visible = false;
          if (p < 1.27) {
            if (label) {
              this.placeLabel(label, to.object.position, 10, -27);
              label.style.opacity = String(1 - (p - 1) / 0.27);
            }
            return true;
          }
          return false;
        }
        updatePath();
        path.getPoint(p, head.position);
        if (label) this.placeLabel(label, head.position, 10, -27);
        for (let i = 0; i < tailSize; i++) {
          path.getPoint(Math.max(0, p - i * 0.012), tip);
          positions.setXYZ(i, tip.x, tip.y, tip.z);
        }
        positions.needsUpdate = true;
        return true;
      },
      finish: (now) => {
        to.flash = now + 400;
        this.burst(to.object.position, tint, now);
      },
      arrivesAt: start + 1200,
      dispose: () => {
        this.scene.remove(tail, head);
        trailGeometry.dispose();
        tail.material.dispose();
        head.material.dispose();
        label?.remove();
      },
    });
  }

  burst(origin, tint, start) {
    const geometry = new THREE.BufferGeometry();
    const speeds = Array.from({ length: 24 }, () => new THREE.Vector3().randomDirection().multiplyScalar(1.3 + Math.random() * 3));
    const positions = new THREE.BufferAttribute(new Float32Array(speeds.length * 3), 3);
    geometry.setAttribute("position", positions);
    const points = new THREE.Points(geometry, new THREE.PointsMaterial({
      color: tint, size: 0.8, map: this.glow, transparent: true, depthWrite: false,
      blending: THREE.AdditiveBlending,
    }));
    points.frustumCulled = false;
    points.position.copy(origin);
    this.scene.add(points);
    this.addEffect({
      tick: (now) => {
        const p = (now - start) / 480;
        if (p >= 1) return false;
        for (let i = 0; i < speeds.length; i++) {
          const v = speeds[i];
          positions.setXYZ(i, v.x * p, v.y * p, v.z * p);
        }
        positions.needsUpdate = true;
        points.material.opacity = (1 - p) ** 2;
        return true;
      },
      dispose: () => {
        this.scene.remove(points);
        geometry.dispose();
        points.material.dispose();
      },
    });
  }

  ripple(node, color, start) {
    const ring = new THREE.Mesh(this.torus, new THREE.MeshBasicMaterial({
      color, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending,
    }));
    this.scene.add(ring);
    this.addEffect({
      tick: (now) => {
        const p = (now - start) / 730;
        if (p >= 1) return false;
        ring.position.copy(node.object.position);
        ring.scale.setScalar(0.4 + p * 4.4);
        ring.material.opacity = 1 - p;
        return true;
      },
      dispose: () => { this.scene.remove(ring); ring.material.dispose(); },
    });
  }

  link(from, to, start) {
    const geometry = new THREE.BufferGeometry();
    const positions = new THREE.BufferAttribute(new Float32Array(6), 3);
    geometry.setAttribute("position", positions);
    const line = new THREE.Line(geometry, new THREE.LineBasicMaterial({
      color: PINK, transparent: true, depthWrite: false,
    }));
    line.frustumCulled = false;
    this.scene.add(line);
    this.addEffect({
      tick: (now) => {
        const p = (now - start) / 950;
        if (p >= 1) return false;
        const a = from.object.position, b = to.object.position;
        positions.setXYZ(0, a.x, a.y, a.z);
        positions.setXYZ(1, b.x, b.y, b.z);
        positions.needsUpdate = true;
        line.material.opacity = 1 - p;
        return true;
      },
      dispose: () => {
        this.scene.remove(line);
        geometry.dispose();
        line.material.dispose();
      },
    });
  }

  caption(node, text, start, social = false) {
    const label = this.bubble(text, social);
    const duration = social ? 2400 : 950;
    this.addEffect({
      tick: (now) => {
        const p = (now - start) / duration;
        if (p >= 1) return false;
        this.placeLabel(label, node.object.position, 10, -27 - (social && !this.motion.matches ? p * 12 : 0));
        label.style.opacity = String(Math.min(1, 3 * (1 - p)));
        return true;
      },
      dispose: () => label.remove(),
    });
  }

  heart(from, to, start) {
    const label = document.createElement("span");
    label.className = "live-heart";
    label.setAttribute("aria-hidden", "true");
    const glyph = document.createElement("span");
    glyph.textContent = "♥";
    label.append(glyph);
    this.overlay.append(label);
    const reduce = this.motion.matches;
    const travel = reduce || from === to ? 0 : 950;
    const point = new THREE.Vector3();
    this.addEffect({
      tick: (now) => {
        const elapsed = Math.max(0, now - start);
        const flight = travel ? Math.min(1, elapsed / travel) : 1;
        const fade = Math.max(0, (elapsed - travel) / 420);
        if (fade >= 1) return false;
        point.copy(from.object.position).lerp(to.object.position, flight);
        point.y += Math.sin(Math.PI * flight) * 4;
        this.placeLabel(label, point, -9, -18);
        if (!reduce) glyph.style.transform = `scale(${1 + 0.36 * Math.sin(Math.PI * fade)})`;
        label.style.opacity = String(1 - fade);
        return true;
      },
      dispose: () => label.remove(),
    });
  }

  instanceBatch(isChannel, count) {
    const threshold = isChannel ? CHANNEL_BATCH_THRESHOLD : USER_BATCH_THRESHOLD;
    let batch = this.instanceBatches.get(isChannel);
    if (count <= threshold) {
      if (batch) batch.visible = false;
      return null;
    }
    if (!batch || batch.instanceMatrix.count < count) {
      if (batch) this.disposeInstanceBatch(batch);
      const geometry = isChannel ? new THREE.TorusGeometry(1, 0.04, 4, 16) : new THREE.SphereGeometry(1, 6, 4);
      batch = new THREE.InstancedMesh(geometry, new THREE.MeshBasicMaterial(), 2 ** Math.ceil(Math.log2(count)));
      batch.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
      batch.frustumCulled = false;
      this.scene.add(batch);
      this.instanceBatches.set(isChannel, batch);
    }
    batch.visible = true;
    batch.count = count;
    return batch;
  }

  disposeInstanceBatch(batch) {
    this.scene.remove(batch);
    batch.dispose();
    batch.geometry.dispose();
    batch.material.dispose();
  }

  draw(now) {
    this.frame = requestAnimationFrame((t) => this.draw(t));
    const reduce = this.motion.matches;
    const t = reduce ? 0 : now / 1000;
    this.sky.rotation.z = t * 0.003;
    const distance = this.cameraDistance();
    this.camera.position.set(Math.sin(t * 0.19) * 2.5, 8 + Math.sin(t * 0.13) * 1.4, distance);
    this.camera.lookAt(0, 0, 0);
    this.camera.updateMatrixWorld();
    this.playPending(now);
    const delta = Math.max(0, Math.min(0.05, (now - (this.lastFrameAt ?? now - 16.7)) / 1000));
    this.lastFrameAt = now;
    const travelling = new Map();
    for (const message of this.channelMessages) {
      if (!travelling.has(message.from)) travelling.set(message.from, message.to);
    }
    for (const [nodes, isChannel] of [[this.channels, true], [this.users, false]]) {
      const batch = this.instanceBatch(isChannel, nodes.size);
      let index = 0;
      for (const [id, node] of nodes) {
        if (isChannel) node.size = THREE.MathUtils.lerp(node.size, node.targetSize, reduce ? 1 : 0.06);
        if (!isChannel) {
          const ring = travelling.get(node) ?? this.channels.get(node.viewing);
          if (ring) {
            node.goal.copy(ring.object.position);
            node.goal.x += Math.cos(node.phase) * ring.size * 0.55;
            node.goal.y += Math.sin(node.phase) * ring.size * 0.46;
            node.goal.z += 1.5;
          } else {
            node.goal.copy(node.home);
          }
        }
        const flash = reduce ? 0 : Math.max(0, node.flash - now) / 700;
        const breath = node.online && !reduce ? 1 + 0.13 * Math.sin(t * 2 + node.phase) : 1;
        const selected = this.selected?.id === id || (isChannel && this.selectedChannels?.has(id));
        const speed = !isChannel && travelling.has(node) ? 21 : 8.4;
        node.object.position.lerp(node.goal, reduce ? 1 : 1 - Math.exp(-speed * delta));
        const displaySize = batch && isChannel ? Math.min(node.size, 33 / Math.sqrt(nodes.size)) : node.size;
        node.visualSize = displaySize * (breath + flash);
        node.core.scale.setScalar(node.visualSize);
        const energy = isChannel ? 1.13 : (node.online ? 1.9 : 0.52) * node.energy;
        node.core.material.color.setHex(isChannel ? GOLD : node.online ? CYAN : 0x758caa)
          .multiplyScalar(energy + flash * 1.7 + (selected ? 1.5 : 0));
        node.halo.scale.setScalar(node.size * (isChannel ? 2.5 : 7));
        node.halo.material.opacity = isChannel ? 0.14 + flash : (node.online ? 0.65 : 0.18) * breath + flash;
        if (selected) node.halo.material.opacity += 0.38;
        node.core.visible = !batch;
        node.halo.visible = !batch || selected;
        if (batch) {
          const transform = this.instanceTransform;
          transform.position.copy(node.object.position);
          transform.quaternion.copy(node.core.quaternion);
          transform.scale.setScalar(node.visualSize);
          transform.updateMatrix();
          batch.setMatrixAt(index, transform.matrix);
          batch.setColorAt(index++, node.core.material.color);
        }
        node.label.classList.toggle("selected", selected);
        this.updateLabel(node, isChannel ? 15 : 9);
      }
      if (batch) {
        batch.instanceMatrix.needsUpdate = true;
        batch.instanceColor.needsUpdate = true;
      }
    }
    this.drawChannelDepartures(now);
    const waiting = new Set();
    let launches = 0;
    this.channelMessages = this.channelMessages.filter(({ from, to, channelID, content, tint, queuedAt }) => {
      if (this.channels.get(channelID) !== to || now - queuedAt > 1600) return false;
      if (launches >= 4 || waiting.has(from) || travelling.get(from) !== to || from.object.position.distanceToSquared(from.goal) > 0.04) {
        waiting.add(from);
        return true;
      }
      launches++;
      this.sendMeteor(from, to, content, now, tint);
      return false;
    });
    this.updateRelationships();
    const arrived = [];
    this.effects = this.effects.filter((effect) => {
      if (effect.arrivesAt !== undefined && now >= effect.arrivesAt) {
        arrived.push(effect.finish);
        effect.arrivesAt = undefined;
      }
      if (effect.tick(now)) return true;
      effect.dispose();
      return false;
    });
    for (const finish of arrived) finish(now);
    this.composer.render();
  }

  drawChannelDepartures(now) {
    for (const node of this.departingChannels) {
      const progress = Math.max(0, now - node.departedAt) / CHANNEL_DEPARTURE_MS;
      if (progress >= 1) {
        this.removeNode(node);
        this.departingChannels.delete(node);
        continue;
      }
      const collapse = progress < 0.35;
      const wave = Math.max(0, (progress - 0.35) / 0.65);
      const scale = collapse ? 1 - progress * 2.3 : 0.2 + wave * 3.2;
      node.core.scale.setScalar((node.visualSize ?? node.size) * scale);
      node.core.material.color.setHex(collapse ? GOLD : PINK).multiplyScalar(2.4);
      node.core.material.opacity = 1 - wave;
      node.halo.material.opacity = 0.65 * (1 - progress);
      this.placeLabel(node.label, node.object.position, 0, -27);
      node.label.style.opacity = String(Math.min(1, (1 - progress) * 3));
    }
  }

  updateLabel(node, offset) {
    const show = node.showLabel || node.label.classList.contains("selected");
    if (!show && !this.pointer) {
      if (!node.label.hidden) node.label.hidden = true;
      return;
    }
    if (!show) {
      const p = this.projected.copy(node.object.position).project(this.camera);
      const x = (p.x + 1) * this.width / 2, y = (1 - p.y) * this.height / 2;
      if (Math.hypot(x - this.pointer.x, y - this.pointer.y) > 15) {
        node.label.hidden = true;
        return;
      }
    }
    this.placeLabel(node.label, node.object.position, 9, offset);
  }

  dispose() {
    cancelAnimationFrame(this.frame);
    this.resizeObserver.disconnect();
    this.motion.removeEventListener("change", this.motionChanged);
    this.stage.removeEventListener("pointermove", this.onPointer);
    this.stage.removeEventListener("pointerleave", this.leavePointer);
    this.stage.removeEventListener("click", this.pickNode);
    this.clearEffects();
    this.select(null, [], []);
    for (const group of [this.users, this.channels]) {
      for (const node of group.values()) this.removeNode(node);
    }
    for (const batch of this.instanceBatches.values()) this.disposeInstanceBatch(batch);
    this.sphere.dispose();
    this.torus.dispose();
    this.sky.geometry.dispose();
    this.sky.material.dispose();
    this.glow.dispose();
    this.bloom.dispose();
    this.outputPass.dispose();
    this.composer.dispose();
    this.renderer.dispose();
    this.renderer.forceContextLoss();
    this.renderer.domElement.remove();
  }
}
