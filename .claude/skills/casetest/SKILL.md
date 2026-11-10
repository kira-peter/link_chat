---
name: casetest
description: 编写 icefire 集成测试用例（casetest）的经验与约束，包含灰盒 DAO 状态验证。当用户要求编写、修改、补充 casetest 测试用例，或新增机器人自动化测试时使用。
---

# 编写 CaseTest 测试用例

CaseTest 是 icefire 的**端到端集成测试框架**：用真实客户端连接（机器人）登录游戏服务器，发送协议、断言服务器响应与 PlayerData 状态。最终接入 CI 持续运行。

代码位置：`icefire-client/src/main/java/com/lc/billion/icefire/client/casetest/`

## 框架速览（写之前必须理解）

| 组件 | 作用 |
|------|------|
| `AbstractCase` | 用例基类，只需实现 `run(CaseContext)`；`caseName()` 默认取实现类简单类名，可选覆写 |
| `CaseContext` | 通过 `newCaseClient(deviceId)` 创建已登录、初始数据就绪的客户端；结束时统一关闭 |
| `CaseClient` | 同步客户端：`sendAndWait` / `waitFor` / `send` / `getPlayerData()` / `gm()` |
| `CaseMeta`（`ctx.meta()` / `meta(ctx, XxxConfig.class)`） | 读取本地 `meta/` 策划配置，复用服务端 `@Config` 配置类，避免硬编码策划值 |
| `GmHelper`（`client.gm(...)` / `client.gm().daoQuery(...)`） | **请求-响应式 GM（首选）**：普通 GM 用于构造状态，`daoQuery` 用于只读验证服务端内存 DAO；均同步等待一条 `GcGM`，`error` 非空自动抛断言。老式 `sendGmAndWait(cmd, GcXxx.class)` 仅兼容存量 |
| `So`（`import static ...So.so`） | 链式断言，失败抛 `CaseAssertionException`，自动附带文件名:行号 |
| Case 自动发现 | 启动时扫描 `casetest.casesuites` 包及子包中的具体 `AbstractCase`；新增用例无需修改中心注册表 |
| `CaseTestMain` | 启动入口，按启动参数或 System properties 注入连接参数（`--web-url` / `--server-id` 或 `case.webUrl` / `case.serverId`，serverId 默认 "1"） |

**典型骨架**：

```java
public class XxxCase extends AbstractCase {
    // 无需实现 caseName()，默认即类名 "XxxCase"
    @Override public void run(CaseContext ctx) {
        CaseClient client = ctx.newCaseClient(0);   // login + waitForReady 已完成
        client.gm().setBuildingLevel(330101, 1);     // GM 把状态重置到已知起点
        GcXxx resp = client.sendAndWait(new CgXxx());
        so(resp.getErrorCode()).isEqualTo(PsErrorCode.SUCCESS.getValue(), "操作成功");
    }
}
```

`sendAndWait`：默认按命名约定 `CgFoo → GcFoo` 推断响应；若不同名（如 `CgLandUpgrade → GcBuildingUpgradeResult`）用显式重载 `sendAndWait(req, GcXxx.class)`。等待目标响应期间，中间推送会自动 apply 到 `PlayerData`，因此断言完响应后可直接 `client.getPlayerData()` 校验状态同步。

新增 Case 时直接在 `casetest.casesuites` 包或其子包创建具体 `AbstractCase` 实现。类必须提供可访问的无参构造器，构造器中不执行登录或业务操作；`caseName()` 默认取类名，覆写时必须全局唯一。框架自动发现并按名称稳定排序，无需注解或手工注册。

## 灰盒 DAO 查询：提高覆盖率与可验证性

CaseTest 已可通过 `client.gm().daoQuery(expression)` 只读查询服务器 MainWorker 当前可见的内存 DAO 数据，并返回 `JsonNode`。编写或补充用例时，应主动判断是否存在仅靠响应和 `PlayerData` 无法可靠验证的服务端状态；存在时，合理增加灰盒断言。

### 适合使用灰盒断言的场景

- 协议只返回成功码，没有返回变更后的完整数据。
- 状态没有客户端推送，或 `PlayerData` 未维护对应字段。
- 需要确认多项副作用同时落入内存实体，例如扣资源、加道具、更新次数和冷却时间。
- 需要区分“响应成功”与“服务端状态确实改变”，或验证失败分支没有误改数据。
- 容器数据很大，只需验证 Map 中指定 Key 或 List 中指定下标。

灰盒断言是黑盒断言的补充，不是替代：优先同时验证**协议结果 + 客户端可见状态 + DAO 内部状态**。不要因为能读 DAO 就跳过错误码、响应字段和必要推送的验证，否则可能漏掉客户端契约回归。

### 查询语法

```java
JsonNode city = client.gm().daoQuery(
        "RoleCityDao.findById($roleId)");
JsonNode x = client.gm().daoQuery(
        "RoleCityDao.findById($roleId).getPosition().getX()");
JsonNode firstOnline = client.gm().daoQuery(
        "RoleDao.findById($roleId).getLast24HOnlineTimes().get(0)");
JsonNode emojiState = client.gm().daoQuery(
        "RoleDao.findById($roleId).getUsableEmoji().get(\"1\")");
```

- 根节点固定为 `<DaoName>.findById(<id|$roleId>)`；`DaoName` 为 **DAO 类的 SimpleName（首字母大写，如 `RoleCityDao`）**，与服务端 `DaoService.daosByDaoName` 同一份索引。当前玩家数据优先使用 `$roleId`，避免硬编码角色 ID。
- 后续仅支持 JavaBean getter/is 字段链，以及 `Map.get(key)`、`List.get(index)`；不允许调用任意业务方法或修改状态。
- Map Key 支持字符串、整数、`123L` Long 和 `$roleId`；List 下标必须是非负整数。
- 可直接查询完整实体，但默认优先查询最小必要字段。大型 Map/List 应使用 `.get(key/index)` 精确取值，避免快照 200 项限制和无关字段耦合。
- 稳定 DTO 可用 `daoQuery(expression, Dto.class)`；Entity 字段易变化时优先用 `JsonNode.path(...)` 只断言关心字段。

### 推荐断言方式

操作前先读取基线，操作后再次查询并验证相对变化，同时保留协议断言：

```java
int before = client.gm().daoQuery(countExpression).asInt();
GcXxx response = client.sendAndWait(new CgXxx());
so(response.getErrorCode()).isEqualTo(PsErrorCode.SUCCESS.getValue(), "操作成功");
int after = client.gm().daoQuery(countExpression).asInt();
so(after).isEqualTo(before + expectedAdd, "服务端内存中的数量按预期增加");
```

- 成功分支验证预期字段发生变化；失败分支应验证关键字段保持不变。
- 优先断言业务结果和不变量，不要大范围比对完整 Entity JSON；后者容易把无关实现细节变成脆弱测试契约。
- 查询当前内存状态不等于验证 MongoDB 已持久化。需要验证落库、重登恢复或跨服同步时，必须设计对应重登/异步完成流程，不能仅凭 `daoQuery` 判定。
- `daoQuery` 是只读工具：测试前置状态仍通过明确的 GM 或正常协议构造，不通过反射篡改 DAO。

### 灰盒 DAO save 审计：`daoSaveWatch`

`daoQuery` 只能证明内存值对不对；**改字段却忘了 `dao.save()` 时内存仍可能正确**。需要验证「是否调用了 save/saveImmediately」时，用 `daoSaveWatch`（业务 Case 不必手写 wasSaved）：

```java
// 成功路径：action 正常返回后，自动断言每个 key 都已 save
client.gm().daoSaveWatch(() -> {
    GcXxx resp = client.sendAndWait(new CgXxx());
    so(resp.getErrorCode()).isEqualTo(PsErrorCode.SUCCESS.getValue(), "操作成功");
}, "RoleCityDao:$roleId", "ItemDao:12345");

// 失败路径：期望未 save
client.gm().daoSaveWatchExpectNone(() -> {
    GcXxx resp = client.sendAndWait(new CgXxx());
    so(resp.getErrorCode()).isEqualTo(PsErrorCode.XXX.getValue(), "应失败");
}, "RoleCityDao:$roleId");
```

- 兴趣 key 形态：`<DaoName>:<id|$roleId>`，`DaoName` 为 DAO 类 SimpleName（首字母大写，如 `RoleCityDao`、`ItemDao`），与 `daoQuery` 共用同一份别名索引。可一次监听多个 DAO；非 Role 维度实体直接写具体 entityId。
- 窗口按 CaseClient（GM 调用方）会话隔离；save 命中只按兴趣 key 匹配，不要求 DAO/实体按 roleId 建模。
- action 抛错时 finally 再调 `daoSaveWatchEnd`（丢弃结果、不断言），避免掩盖业务失败。
- 常规路径禁止手写底层 `daoSaveWatch` GM / `End` / `wasSaved`；防泄漏靠客户端封装的 finally end（不做服务端 TTL）。
- 本能力只证明「调用了 save」，不等于 Mongo 已落库。

## 约束一：可重入（CI 反复跑也要稳定通过）

用例不能依赖「第一次跑」或「干净环境」的隐含前提，必须每次运行都能独立通过。

- **优先用一次性角色**：`ctx.newCaseClient(0)` 会用一次性 deviceId 注册全新角色，天然可重入，首选。
- **使用固定机器人时（`deviceId != 0`）**：该角色在服务器上**已有历史数据**，不要假设它是空白的。用例**开头必须用 GM 把相关状态显式重置到已知起点**（如 `setDroneLevel(1, 0)`、`setBuildingLevel(id, 1)`），再开始断言。
- **不要依赖服务器内部数据状态**：用例不能依赖服务器的隐含运行态（如是否开服第一天、是否处于某活动期、全服计数/排行榜的当前值等）。这类状态在 CI 不同时间运行会变化。需要某种前提时，用 GM 显式构造，或在断言中容忍其浮动（用相对关系而非绝对值）。
- **不要硬编码策划数值**：meta 配置（消耗、数量、上限、segmentCount、属性值、外观 id 等）策划会调整。
  - 优先通过 `ctx.meta().getConfig(XxxConfig.class)` 或基类便捷方法 `meta(ctx, XxxConfig.class)` 读取本地 `meta/`，复用服务端 `@Config` 配置类。
  - `CaseMeta` 默认从 `-Dcase.metaDir=` 指定目录读取；未指定时从运行工作目录向上查找 `meta/`。在 `icefire-client` 下运行时通常会自动找到仓库根目录的 `meta/`。
  - 能从服务端响应读取的运行态，也可以读取响应；但配置驱动的期望值不要写死在 Case 里。
  - 确实需要业务常量时，定义为 `private static final` 并在注释中说明稳定来源；不要把会随 meta 调整的字段值作为常量。
  - 断言尽量用**相对关系**（如「升级后 segment 归零」「消耗后数量减少」）而非绝对数值，减少对具体数值的耦合。
- **不要依赖用例间的执行顺序**：每个用例自带的 `CaseContext` 独立，用例之间不共享状态。

## 约束二：控制机器人数量（覆盖关键路径，不追求 100%）

创建机器人（登录+初始化）有真实开销，要权衡重要性与性价比。

- **覆盖关键路径**：聚焦核心成功流程 + 重要错误码分支，不为了覆盖率而枚举所有边界。
- **一个 client 复用做多段断言**：同一用例内尽量复用同一个 `CaseClient`，用 GM 重置状态在多个子场景间切换（参考 `DroneUpgradeCase` 的「测试一/二/三」），而不是每段都新建 client。
- **仅在状态前提不可复用时才新建 client**：例如「功能未解锁」必须用全新角色，无法在已解锁的 client 上复现。
- 评估时问自己：这条分支出问题的概率 × 影响，是否值得一个机器人的开销？

## 约束三：修改 game 代码的边界

写用例常需要配套改动游戏服务端，按风险分两类处理：

- **可直接执行，事后汇总**（低风险、纯测试支撑）：
  - 新增 GM 命令（测试需要的状态构造/重置能力）。
  - 明确的 bugfix（用例暴露出的服务端缺陷）。
  - 新增错误码协议（用例需校验某错误分支，但服务端尚无对应错误码，如在 `PsErrorCode` 中新增并让服务端返回）。
  - 完成后在回复中**汇总列出所有 game 侧改动**（文件、改了什么、为什么）。
- **必须先与用户确认**（高风险）：
  - 涉及设计变更、跨模块重构、数据结构/线程模型调整。
  - 改动可能影响线上逻辑、而非仅测试路径。
  - 这类改动属于 CLAUDE.md 中「必须有 spec」的范畴，先确认方案再动手。

## 约束四：GM 一律走请求-响应式（casetest 取数/确认的唯一方式）

核心原则：**用例通过 `client.gm(...)` 拿数据、确认完成，不要依赖 `GcNoticeInfo` 等不稳定推送来取值或判断 GM 是否执行完。** 请求-响应式 GM 一发一收、结果由 `GcGM.result` 明确带回，天然确定、可断言。

### 新造 GM：必须用 `GmHandler` 方案（不要再往 big switch 加 case）

在 `icefire-game/.../msg/handler/impl/gm/GmHandler.java` 加一个 `@Gm("xxx")` 方法即可，**无需**改 switch、无需显式注册、无需给 `GmHelper` 加专用封装方法——用例直接 `client.gm("xxx arg1 arg2")`。返回类型决定同步/异步：

- **同步、要回结构化数据**：
  ```java
  @Gm("myGm")
  public GmResult myGm(Role role, GmArgs args) {
      if (!args.isInt(0)) return GmResult.error("usage: myGm <id>");
      // ... 构造/读取状态 ...
      return GmResult.ok(json);   // 用例侧 client.gm("myGm 123").getResult() 读到 json
  }
  ```
- **异步、需等操作真正完成再回**（避免"发了 GM 但异步还没跑完就断言"）：
  ```java
  @Gm("myAsyncGm")
  public void myAsyncGm(Role role, GmArgs args, GmContext ctx) {
      srvDpd.getAsyncOperService().execute(() -> {
          try { ctx.respondResult(result); }        // 成功路径回包
          catch (Exception e) { ctx.respondError(e.getMessage()); }  // 失败路径也必须回包
          return false;
      });
  }
  ```
  框架已自动 `deferAsync`（不写 `ctx.deferAsync()`），但**成功与失败两条路径都必须最终 `respond*`**，否则用例会超时。
- 取参用 `GmArgs`（`getInt(i)`/`isInt(i)`/`get(i)`/`size()`）；命令受 `CgGM` 的 dev 门禁保护，仅 dev 生效。样例模板见 `GmHandler` 里的 `gmEcho`（同步）与 `gmAsyncEcho`（异步）。

### 用到的老 GM 还在 big switch 里：顺手迁移到 `GmHandler`

当用例需要改动一个还在 big switch 里的老 GM 时（填充 `result`、补异步回包、功能调整、或它本身有 bug 需要修复等），就**借这次改动把它从 `CgChatRoomSendMessageHandler.processCommand` 的 `case` 迁到 `GmHandler` 的 `@Gm` 方法**，顺手把它落到新规范。

- **本次目的所需的改动放心做**：该填的 `result`、该补的异步回包、该修的 bug、该改的功能，都正常改。
- **但要细心，别顺手动与本次目的无关的部分**：原有的 `debugMsg`/日志/边界判断/无关的错误分支等，原样保留，不顺手优化、不删旧提示、不改无关副作用。
- 具体来说：迁移主要是位置搬家 + 签名适配（`case` 体 → `@Gm` 方法体），再叠加本次目的所需的改动（回包：同步 `return GmResult.ok(...)`、异步 `ctx.respondResult(...)`；或 bug 修复）。
- 迁移后删掉 switch 里对应的 `case`；因为 `processCommand` 的 `default` 已委托 `GmRegistry`，老的 `#xxx` 文本形式仍然可用（走注册表），不会失效。
- GM 仅 dev 生效；迁移后用改造的用例实机验证一遍，确认行为符合预期（本次有意的改动之外，其余行为与迁移前一致）。
- 迁移属于改动线上代码路径，但限于 dev-only GM、且是本约束明确认可的方向，可直接执行、事后汇总；若发现该 GM 逻辑复杂或牵连非 GM 业务，按约束三「必须先与用户确认」处理。

> 说明：老式在 switch 加 `case "#xxx"` + `GmHelper.sendGmAndWait(cmd, GcXxx.class)` 的做法仅用于读懂存量，**新用例不要再用**。

## 约束五：断言报错要自带完备上下文

目标：CI 失败时，**看日志就能定位问题，不必回到测试代码重新理解**。

- **`So` 已自动附带 `(文件名:行号)`**，但**每个断言都要传 `msg`** 描述「期望什么」，例如 `so(x).isEqualTo(2, "训练后等级为 2")`。
- **失败信息包含关键现场数据**：复杂断言前，把相关上下文拼进 msg（如 `"第 " + attempt + " 次训练后 level=" + level + " seg=" + seg`），而不是只断言一个裸值。
- **GM/等待超时要带命令与目标类型**：参考 `GmHelper.sendGmAndWait` 捕获超时后重抛 `"GM 命令超时 cmd=... 等待=..."`，让失败信息自解释。
- **对错误码断言时**：用 `PsErrorCode.XXX.getValue()` 比较，msg 写清「在什么前提下应返回什么错误码」。
- 需要新的断言类型时，优先在 `So` 中扩展（保持失败出口统一带 file:line），不要在用例里写裸 `if + throw`。

## 约束六：Game 启动与重启权限（进程归属原则）

测试需要本地有一个 Game 节点在线（CaseClient 默认连 serverId=1，可用 `-Dcase.serverId=` 覆盖）。核心原则：**谁启动的进程，谁才有权重启它。**

> **简化前置判断**：直接假设 Web 登录服、MongoDB、Redis、ZooKeeper 等依赖服务都已正常运行，**不要逐个探测它们的就绪状态**，以缩短判断链路。只需关心 `GameMain` 进程本身。

**检测 Game 是否在线**：用进程级探测（不受启动方式影响）：

```bash
ps -ef | grep GameMain | grep -v grep
```

- **有进程** → 直接使用，记为「用户事先启动」。
- **无进程** → 由你启动一个 1 服节点，并记下「此节点由我启动」：
  - 优先从用户当前 IDE 的启动配置中识别 Game 启动项（入口 `GameMain`、服务器编号 1）并沿用其编译前置、`cwd`、环境变量、`vmArgs`。
  - 若无法可靠识别，使用下方模板启动（来自 VS Code/Cursor `launch.json` 的 `Game1`，仅作参考；其他 IDE 等价配置即可）：

```jsonc
{
  "name": "Game1",
  "mainClass": "com.lc.billion.icefire.game.GameMain",
  "projectName": "icefire-game",
  "cwd": "${workspaceFolder}/icefire-game",
  "preLaunchTask": "game: compile",   // mvn compile -Dmaven.test.skip=true -q（在 icefire-game 目录）
  "env": { "LS_ENV_KEY_GAMESERVER_ID": "1" },
  "vmArgs": [
    "--add-opens=java.base/java.lang=ALL-UNNAMED",
    "--add-opens=java.base/jdk.internal.perf=ALL-UNNAMED",
    "--add-exports=java.base/jdk.internal.perf=ALL-UNNAMED",
    "-XX:-OmitStackTraceInFastThrow",
    "-Dio.netty.leakDetectionLevel=advanced"
  ]
}
```

**用工具的后台机制启动**（如 Shell 的 `block_until_ms: 0`），**不要用 `nohup ... &`**——后者子进程会在该次命令结束时被回收而中途死亡。命令行等价启动（cwd 为 `icefire-game`）：

```bash
LS_ENV_KEY_GAMESERVER_ID=1 java \
  --add-opens=java.base/java.lang=ALL-UNNAMED \
  --add-opens=java.base/jdk.internal.perf=ALL-UNNAMED \
  --add-exports=java.base/jdk.internal.perf=ALL-UNNAMED \
  -XX:-OmitStackTraceInFastThrow -Dio.netty.leakDetectionLevel=advanced \
  -cp "target/classes:conf:$(cat target/cp.txt)" \
  com.lc.billion.icefire.game.GameMain
```

（`target/cp.txt` 由 `mvn -pl icefire-game dependency:build-classpath -Dmdep.outputFile=target/cp.txt -q` 生成；若上游模块未装入本地仓库，先 `mvn install -pl icefire-game -am -DskipTests -q`。）

**判断启动成功（重要：查日志文件，别盯标准输出）**：游戏 DEBUG 日志量极大，实时流式监控标准输出会被刷屏甚至截断，低效且不可靠。统一用 logback 文件日志判断：

- 成功标志：游戏日志中出现 `has been started successfully`。
- 日志位置：生效的 logback（`target/classes/logback.xml`）配置 `LOG_DIR=./logs`，即写到**游戏 cwd 下的 `logs/`**，所以路径是 `icefire-game/logs/game.log`。
  > 注意：`logs/` 被 `.gitignore`（`*/logs/*`）忽略，IDE 文件树/源码管理视图默认看不到，但**文件确实存在**，直接按路径打开或用命令读取即可。
- 轮询命令（返回 >=1 即启动完成；冷启动约 1~2 分钟，期间每隔 ~20s 查一次即可）：

```bash
grep -a -c "has been started successfully" icefire-game/logs/game.log
```

- 辅助：`icefire-game/logs/error.log` 看是否有真正的 ERROR（启动期的「实体索引过多」是 INFO，可忽略）。

**测试过程中改了服务端代码、需要重启时**：先用 `ps -ef | grep GameMain` 判断进程归属。
- **我启动的** → 有权限直接重启；重启后同样用 `logs/game.log` 的成功标志确认就绪，再继续测试（除非遇到约束三中「需用户确认」的 game 改动，则先停下确认）。
- **用户事先启动的** → **不擅自重启**，提示用户「需重启服务器以加载改动」，由用户决定是否重启后再继续。

## 约束七：运行顺序与已有 Case 失败处理（变更隔离原则）

**运行 CaseTest**（cwd 为 `icefire-client`）：

```bash
java --add-opens=java.base/java.lang=ALL-UNNAMED \
  --add-opens=java.base/jdk.internal.perf=ALL-UNNAMED \
  --add-exports=java.base/jdk.internal.perf=ALL-UNNAMED \
  -XX:-OmitStackTraceInFastThrow \
  -cp "target/classes:conf:src/main/resources:$(cat target/cp.txt)" \
  com.lc.billion.icefire.client.casetest.CaseTestMain \
  --web-url=http://127.0.0.1:8080/api/warz/v1/login \
  --server-id=1 \
  --only=DroneUpgradeCase
```

- `target/cp.txt` 由 `mvn -pl icefire-client dependency:build-classpath -Dmdep.outputFile=target/cp.txt -q` 生成。
- **`--only=XxxCase` 或 `-Dcase.only=XxxCase` 只运行指定用例**（框架已内置该过滤；按 `caseName` 忽略大小写匹配，逗号分隔多个，如 `--only=DroneUpgradeCase,DroneCenterUnlockCase`）；**省略该参数则全量运行**。
- 连接参数默认 `webUrl=http://127.0.0.1:8080/...`、`appId=com.lc.gos`、`serverId=1`，可用 `--web-url=` / `--app-id=` / `--server-id=` 或 `-Dcase.xxx=` 覆盖；启动参数优先级高于 System properties。

**运行顺序**：
1. **先只跑本次新建/修改的 Case**：加 `-Dcase.only=XxxCase`，先让变更的 Case 稳定通过。
2. 变更 Case 通过后，**去掉 `-Dcase.only` 全量跑一遍**，确认没有引入回归。

**当出现「非本次变更」的 Case 失败时**：
- **初步分析失败原因**（是变更引入的副作用，还是该 Case 自身/环境问题），把分析结论告知用户。
- **由用户确认是否修复、如何修复**——**不要擅自修改已有 Case，也不要改动其他不相关的业务逻辑代码**。
- 只有在用户明确同意后，才按其指示处理。

## 编写流程清单

1. 确认是否有对应 OpenSpec change（新功能必须先有 spec，见 CLAUDE.md）。
2. 阅读目标协议（`Cg*`/`Gc*`）和相关 meta 配置，确认错误码、字段、数值来源。
3. 在 `casesuites/` 下新建 `XxxCase extends AbstractCase`。
4. 用例开头用 GM 重置状态到已知起点；断言成功路径 + 关键错误分支。按需用 `daoQuery` 记录操作前基线并验证操作后的 DAO 状态，同时保留协议和 PlayerData 断言。
5. 若需要新 GM：用 `GmHandler` 的 `@Gm` 方案（同步返 `GmResult`、异步 `void+GmContext`）；用到的老 GM 若还在 big switch 且靠不稳定推送确认，顺手迁移到 `GmHandler` 并改造为回 `result`。bugfix/错误码仍按约束三处理。
6. 确认 Case 位于 `casetest.casesuites` 包或子包、可通过无参构造器实例化，并且 `caseName` 唯一；无需手工注册。
7. 自检：可重入吗？数值有没有写死？是否依赖隐含运行态？是否应增加灰盒断言？是否只查询必要字段？机器人用量是否最小？断言 msg 是否完备？
8. 确保 `GameMain` 进程在线（按约束六处理启动/重启）。
9. 先只跑变更 Case，通过后再全量跑（按约束七处理已有 Case 失败）。
10. 回复用户时汇总：新增/修改了哪些用例、哪些 game 侧改动。
