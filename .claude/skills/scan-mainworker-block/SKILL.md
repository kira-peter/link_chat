---
name: scan-mainworker-block
description: 扫描 MainWorker 主业务线程上的同步阻塞调用（Redis / HTTP / DB / RPC / 等待），产出完整违规清单报告。当用户要求扫描/审计 MainWorker 阻塞调用、排查阻塞主线程的同步 I/O 时使用。
---

# 扫描 MainWorker 阻塞调用

核心架构约束（`GUIDES.md` `Common-Async-R002`）：**禁止在全进程唯一的 MainWorker 上执行 Redis / HTTP / DB / RPC 或显式等待等阻塞调用**。合法的阻塞必须包进 `AsyncOperation` 异步上下文（调用线程 `init()` → 异步线程 `run()` 做 I/O → MainWorker `finish()` 回写业务状态）。

本 skill 把一次性审计方法固化为可复用流程，对 `*Handler` / `*ServiceImpl` 做全量扫描，回溯每个命中点的线程上下文，最终产出一份**完整的违规清单报告**。

> 本 skill 由一次性 MainWorker 阻塞调用审计沉淀而来（原审计快照已并入本流程）。每次执行都应基于当前代码重新扫描，不要照抄任何历史结论。

## 判定基线（关键）

- **默认 MainWorker 原则**：`*Handler` / `*ServiceImpl` 中**未显式异步包装**的方法体，一律默认运行在 MainWorker。
- 这类方法体内**直接**出现同步 Redis I/O / 同步 HTTP / `Thread.sleep` → **判定为违规**。
- 只有当命中点**确实处于异步上下文**（见下方步骤 3）时才排除。
- **不要写死行号**：行号会过期。报告中可写"方法名 / 触发入口"，行号在执行扫描时实时定位、临时记录即可，不要固化进任何产物。

## Step 1：确定扫描范围

范围固定为 `icefire-game/src/main/java` 下所有：

- `*Handler.java`（客户端消息入口，几乎必在 MainWorker）
- `*ServiceImpl.java`（业务编排层）

> 其他后缀（DAO / Manager / Ticker / Operation）**不是本次主扫描目标**，但当它们出现在违规调用链路上、用于判定 `*Handler`/`*ServiceImpl` 命中点是否真违规时，需要顺着读。

## Step 2：grep 关键模式

用 Grep 工具（不要写死行号）在上述范围内逐类模式搜索，建议 `output_mode: content` 带 `-n` 以便临时定位：

**Redis 同步 I/O**

```
(localLettuceClient|webLettuceClient|redisClient|gameRedisClient|gameJedisClient|gameLettuceClient)\.\w+\(
```

补充：`\.zadd\s*\(`、`\.zrange`、`\.hget`、`\.hset`、`\.get\(`、`\.set\(`、`\.del\(`、`\.incr` 等同步签名（注意区分同名的异步 API，见下）。

> **同步 vs 异步辨别**：客户端通常同时提供同步与异步两套 API。带 `async` 前缀（如 `asyncGet` / `asyncSet`）、返回 `CompletableFuture` / `RedisFuture`、或接收回调 `Consumer<...>` 的，是异步 API，**不算阻塞**。裸 `get/set/del/hget/zadd` 等返回具体值的才是同步阻塞。

**HTTP 同步调用**

```
HttpUtil\.|HttpClients\.|HttpURLConnection|client\.send\(
```

补充：`postJSON`、`get(`、`execute(` 等同步发送；`sendAsync(` 是异步（不算阻塞）。

**DB / RPC 同步调用**

```
dbFind|dbFindBy|collection\.(find|save|remove)|rpcService\.getProxy|proxy\.\w+\(
```

补充：只把真实 Mongo 查询 / 写入和同步 RPC 计为阻塞；DAO 内存 `findById`、只组装 Proxy 或已经位于异步 Operation 的调用不算违规。

**线程阻塞**

```
Thread\.sleep\b
```

补充：`.get()` / `.join()`（阻塞等待 Future）、`CountDownLatch.await`、`.acquire(`（信号量）等显式阻塞等待，命中后也按同样规则回溯。

## Step 3：回溯调用栈，判定是否在异步上下文

对每个命中点，向上回溯调用链（用 Grep 找调用方 / Read 读方法体），判断它**实际**运行在哪个线程。命中点若处于以下**任一**异步上下文，则**不算违规**：

1. **`AsyncOperation.run()`** 内 —— 含子类 `AsyncIOThreadOperation` / `BindThreadOperation` 等。`run()` 跑在异步 I/O 线程，是合法位置；`init()` 在调用 `execute` 的线程（业务入口通常为 MainWorker），`finish()` 回 MainWorker，其中的阻塞**仍违规**。
2. **`asyncOperService.async(Runnable)`** / **`asyncOperService.execute(AsyncOperation)`** 提交的任务体内（常见获取方式 `srvDpd.getAsyncOperService()`）。
3. **独立线程池**：`Executors.newXxxThreadPool(...)` / `ScheduledExecutorService` / `asyncScheduleAtFixedRate` 等自管理线程上执行的回调。
4. **`@Scheduled`** Spring 调度方法（跑在 Spring 调度线程，非 MainWorker）。
5. **Redis Pub/Sub 订阅线程**：`JedisPubSub.onMessage(...)` 回调链路（跑在 Jedis 订阅线程）。

判定要点：

- **看"实际执行线程"而非"方法签名"**。一个 `*ServiceImpl` 方法即使本身没包装，但**所有调用方**都在异步上下文里调它，则它运行时不阻塞 MainWorker——但这仍属于"契约误导"（见下方风险分级），应列入报告并建议改名/内部显式包装。
- **多入口方法要逐入口判定**：同一方法可能既被 GM/Handler（MainWorker）直接调用、又被定时器（调度线程）调用。只要存在一条 MainWorker 入口，就构成违规。
- **`@RequireMainWorker` 注解**（`com.lc.billion.icefire.game.annotation.RequireMainWorker`）是强信号：被它标注的方法/类**确定在 MainWorker**，其内的阻塞调用必然违规。

## Step 4：风险分级

- **明确违规**：MainWorker 入口直接同步阻塞 → 会卡住同 worker 上所有玩家消息（登录 / 战斗 / 排行榜路径尤其严重）。
- **契约误导（潜在地雷）**：`*ServiceImpl` 方法当前虽跑在异步线程或无调用方，但未显式异步包装、或公开了同步重载，后续维护者极易误用 → 列入报告，标注风险。
- **已正确包装**：处于 Step 3 异步上下文 → 不违规，仅列入复核清单供排除。

## 输出：完整违规清单报告

产出一份 Markdown 报告（默认在回复中给出）。审计结果是**时点快照**，不要写入 `docs/` 知识库；如用户要求落盘，写到用户指定路径（如 `reports/{YYYYMMDD}-mainworker-blocking-call-audit.md`）。报告含以下四节：

### 一、明确违规清单

表格列：**序号 | 文件 | 方法/触发入口 | 违规调用 | 调用链路 | 风险**。

| # | 文件 | 方法 / 触发入口 | 违规调用 | 调用链路 | 风险 |
|---|------|----------------|----------|---------|------|
| 1 | `.../xxx/CgXxxHandler.java` | `handle(...)` → GM `#xxx` | `HttpUtil.postJSON(...)` 同步 HTTP | Handler 直接同步执行，无 `async()` 包裹 | MainWorker 卡在 HTTP 等待，阻塞同 worker 所有玩家消息 |

- 文件用仓库相对路径，**不带行号**。
- "调用链路"写清从入口到命中点的关键链条，并点明为何判定为 MainWorker。
- "风险"具体到影响面（哪条玩家路径被阻塞、阻塞时长等）。

### 二、整改建议

按**每条违规**给出可执行方案，优先两种范式：

1. **整段包进异步 I/O 操作**：

```java
srvDpd.getAsyncOperService().execute(new AsyncIOThreadOperation() {
    @Override
    public boolean run() {
        // 同步 Redis / HTTP / sleep 移到这里（异步 I/O 线程）
        return true;
    }
    @Override
    public void finish() {
        // 如需回写 Role/World 状态，放这里（回到 MainWorker）
    }
});
```

2. **改用异步回调 API**：如 Redis `asyncGet(...)` 回调、`HttpClient.sendAsync(...).thenAccept(...)`、已有的带 `Consumer<...>` 异步重载等。

针对"契约误导"项，额外建议：方法内部显式 `asyncOperService.async(...)` 包裹、或改名（如 `onXxxEvent`）让线程契约自解释、或删除/`@Deprecated` 危险的同步重载。

### 三、已正确异步包装（复核用）

列出被 Step 3 合法异步上下文包裹的命中点（按文件 + 方法名 + 所属异步上下文类型），供复核排除，**不带行号**。例如：

- `XxxServiceImpl`：`fooAsync`（`asyncOperService.async(...)` 内）
- `RedisSubscriberServiceImpl`：订阅回调（独立订阅线程）
- `WorldServiceImpl`：`@Scheduled(cron=...)` 触发的 `Thread.sleep`（Spring 调度线程）

### 四、扫描方法说明（可选）

简述本次实际使用的 grep 模式与判定基线，便于他人复跑。

## 执行清单

1. 用 Grep 在 `*Handler.java` / `*ServiceImpl.java` 范围内跑 Step 2 的全部模式，收集命中点（带 `-n` 临时记录位置）。
2. 逐命中点按 Step 3 回溯调用栈，标注实际执行线程与是否处于异步上下文。
3. 按 Step 4 分级：明确违规 / 契约误导 / 已正确包装。
4. 产出四节报告：违规清单（含调用链路与风险）、逐条整改建议、已包装复核清单、方法说明。
5. 全程**不写死行号**；报告用文件路径 + 方法名定位。
6. 本 skill 只做审计与报告，**不自动修改业务代码**；如需整改，按 `AGENTS.md` 判断是否需要 OpenSpec change。
