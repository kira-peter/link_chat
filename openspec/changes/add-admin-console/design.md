# Design

## Context

动机见 proposal.md 的 Why 部分。与本设计相关的现状：

- **入口**：服务端只有一个 `http.ServeMux`（`pkg/server/server.go:61`），上面挂着 `/ws` 和 `/healthz`。
- **业务通道**：所有业务都走 WebSocket 信封协议。
- **认证**：令牌是 32 字节随机数，只把 SHA-256 存进 `tokens.json`，并已有 `purpose` 字段，目前取值为 `session` 或 `reset`。恢复会话由 `userForToken`（`handlers.go:355`）校验。
- **存储**：`Storage` 接口后面是单 goroutine actor，数据全量放在内存里。元数据整文件原子重写，消息按会话写成 `.jsonl` 追加日志。所有读写都串行进行，一次长时间扫描会卡住整站发消息。
- **Hub**：同样是单 goroutine actor，持有 `conns`、`online`、`subs`。广播的做法是先在 Hub 内取快照，再到 Hub 外入队。
- **日志**：用自定义 `slog.Handler`（`logfmt.go`）输出到 stderr，内存里不保留。
- **前端与权限**：仓库里没有任何前端工程，也没有角色或权限概念。

## Goals / Non-Goals

**Goals:**
- 后台只交付一个服务端二进制，不引入前端构建链，也不需要额外进程。
- 所有鉴权都在服务端按每个请求完成。前端只负责展示，不承担任何安全职责。
- 后台的任何读操作不能明显拖慢聊天，尤其是搜索。

**Non-Goals:**
- 不做多实例部署，也不做共享会话。
- 不在 TUI 客户端里提供管理功能。
- 不做消息导出或批量删除，不做按处分自动清理内容。
- 不做全文索引，搜索沿用顺序扫描，见风险一节。
- 不处理 GDPR 类的删除请求。

## Decisions

### 1. 后台挂在同一个 mux 上，使用 HTTP JSON 加 SSE，不复用 WebSocket 信封

`/admin/` 下有三类路由，都包一层鉴权中间件：

- 静态页：通过 `go:embed` 嵌入 `pkg/server/adminweb/`。
- REST 风格的 JSON 接口：`/admin/api/*`。
- 实时推送：`/admin/api/events`，使用 SSE。

**理由**：后台的请求大多是一次性查询，用 HTTP 更直接。cookie 鉴权和 CSRF 防护在 HTTP 上有成熟做法。SSE 是单向推送，用浏览器原生的 `EventSource` 就能实现断线重连，不需要像 WS 那样做握手和心跳。

**备选**：复用 `/ws` 加 admin 操作。这样做会把管理请求混进聊天协议，聊天连接的限流、单会话和订阅模型也得给后台开例外，因此放弃。另起一个端口也被否决，因为用户要求部署简单。

### 2. 权限存在用户记录上

`storage.User` 增加两个字段：

- `perms []string`
- `super bool`

两者都用 `omitempty`，因此老数据读进来就是没有权限，不需要迁移。

权限常量定义在 `pkg/server/admin_perms.go`。超级管理员的有效权限恒为全集。

授权检查写成纯函数 `canGrant(actor, target, newPerms) error`，规则如下：

- 不能修改自己的权限。
- 目标是超级管理员时一律拒绝。
- 新增的权限必须是 actor 持有的。
- 只有超级管理员能动 `accounts.manage`。

这样可以针对这个函数穷举测试。

**备选**：单独建一个 `admins.json`。这样每次查询都要多一次关联，后台建号和聊天账号也会割裂，因此放弃。

### 3. 后台令牌复用令牌表，新增 `purpose=admin`

后台令牌和聊天令牌放在同一张令牌表里，具体做法：

- 签发、哈希、落盘都复用 `newToken`、`tokenHash`、`SaveToken`，有效期 12 小时。
- `userForToken` 改为接收期望的 `purpose`，用途不符就当作不存在处理。这样聊天令牌和后台令牌不会互相冒用。
- `DeleteSessionTokensForUser` 只删 `session` 用途的令牌。因为单会话约束只作用在聊天会话上，所以后台令牌天然不会受影响。
- 新增 `DeleteTokensForUserPurpose`，封禁和重置密码时用它作废 `admin` 令牌。

cookie 的设置：

- 名称为 `lc_admin`，带 `HttpOnly`、`SameSite=Strict`、`Path=/admin/`。
- 配置了 TLS，或者 `trust_proxy` 且 `X-Forwarded-Proto=https` 时，加上 `Secure`。

CSRF 防护：

- `SameSite=Strict` 已经挡住了跨站携带 cookie。
- 所有非 GET 请求还要求带 `X-Requested-With: link-chat-admin` 头，且 `Content-Type: application/json`。这两个条件让跨站的简单表单提交无法构造出合法请求。

**鉴权中间件** `adminAuth(perm)` 对每个请求都做以下检查：

1. 按 cookie 查令牌。
2. 读出当前用户。
3. 检查用户没有被封禁。
4. 检查用户的有效权限包含所需权限。

只要有一项不满足，就返回 `401` 或 `403`，响应体用 `{code, message}` 格式，与协议错误码保持一致。

这些读取都走存储 actor 的内存查询，代价可以忽略。正因为每次都查，权限的变化会立即生效。

### 4. 处分存成独立的 `sanctions.json`，生效状态惰性计算

每条处分的结构：

```
Sanction{ID, UserID, Kind(ban|mute), Reason, CreatedBy, CreatedAt,
         ExpiresAt(0=永久), RevokedBy, RevokedAt, SupersededBy}
```

**生效条件**：`RevokedAt==0 && SupersededBy=="" && (ExpiresAt==0 || now<ExpiresAt)`。

存储层提供一个方法 `ActiveSanction(uid, kind, now)`。它在 actor 内维护一个 `uid → []sanction` 的索引，所以登录和发消息路径上的检查是 O(该用户的处分数)。

**写新处分**：在同一次 actor 操作里，把旧的生效处分标记为被取代，再写入新处分。这样不会出现两条同时生效的处分。

**到期处理**：不需要定时器。下一次检查时，过期的处分自然就不再生效。

**到期推送**：禁言自然到期时服务端不推送任何事件。客户端在收到 `account.muted` 时就拿到了 `expires_at`，到时间后自己恢复输入。即使客户端时钟有偏差，最坏的情况也只是发送时再收到一次 `muted`。

**插入点**：

- **封禁检查**：
  - `opLogin`：密码校验通过之后、签发令牌之前。
  - `opResume`：令牌校验之后。
  - 找回密码第一步：返回 `cannot_recover`。
  - 后台登录与 `adminAuth`。
- **禁言检查**：`opMessageSend`、`opMessageEdit`、`opChannelCreate` 的入口，放在成员校验之后。

**执行封禁**，按以下顺序：

1. 写入处分记录。
2. 作废 `session` 和 `admin` 令牌。
3. 用 `hub.connectionsForUser(uid)` 取得该用户的所有连接，对每条连接先入队 `account.banned` 帧，再关闭连接。
4. 关闭该用户的 SSE 事件流。

第 3 步复用 `notifyDisplaced`（`handlers.go:230`）的模式，抽出一个通用的 `notifyAndClose(uid, frame)`。原有的顶号和这次新增的强制下线都改为调用它。

### 5. 审计日志存成追加写的 `audit.jsonl`

每条记录的结构是 `{at, actor_id, actor_name, action, target, detail}`，其中 `detail` 是一个小对象，例如搜索时的搜索条件、处分的时长和原因。

写入方式：

- 写入走存储 actor 的追加路径，复用 `appendLine`。
- 内存里只保留最近 5000 条，供后台列表展示。更早的记录按需从文件尾部倒着读。
- 存储接口不提供删除或修改审计记录的方法。

命令行建超级管理员时，审计记录的 actor 写为 `cli`。

### 6. 搜索在存储 actor 内分片执行

`SearchMessages(filter, cursor)` 的执行方式：

- 只扫描内存中已经载入的消息，不读磁盘。现有实现启动时会把全部数据载入内存，消息也在其中。
- 遍历顺序是按会话、在会话内按 seq 从新到旧。
- 每次调用最多扫描 20 000 条消息，最多返回 100 条结果。没扫完时返回游标 `{convIdx, seq}`。
- 前端的「继续搜索」就是用这个游标再调用一次。

这样每次占用 actor 的时间有上限，在 20 000 条的量级下约为毫秒级，发消息只会被短暂插队，不会被长时间阻塞。

会话的遍历顺序按会话 ID 排序后固定，保证游标稳定。

匹配规则：大小写不敏感的子串匹配，已删除的消息跳过。

**备选**：为搜索另起一个 goroutine 读快照。现有数据结构没有快照机制，为此引入锁会破坏 actor 模型，因此放弃。

### 7. 实时事件：Hub 增加后台订阅者集合

`hubState` 新增 `admins map[*adminSub]struct{}`。每个 `adminSub` 包含：

- 用户 ID
- 一个带缓冲的 chan
- 最近一次刷新的权限快照

**钩子**：新增 `s.adminEmit(ev)`，在以下位置调用：

- 发消息、编辑消息、删除消息成功之后，即广播之后（`handlers.go` 中 `broadcast` 调用的旁边）。
- 上线、下线（`server.go:238`、`handlers.go:194`）。
- 注册、登录成功。
- 处分变化。

**分发**：在 Hub 内对订阅者取快照，在 Hub 外按每个订阅者的权限裁剪事件后非阻塞投递。

- 对没有 `chat.view` 的订阅者，消息事件只保留 `{type:"message.count"}`，去掉正文和会话。
- 投递时如果缓冲已满，就关闭该订阅者。前端的 EventSource 会自动重连，重连后重新拉取快照，因此不需要保证每个事件都送达。

**权限刷新**：每条 SSE 连接每 15 秒发一次心跳注释，顺便重新做一次鉴权。另外，权限变更和封禁会主动调用 `hub.closeAdminSubs(uid)`。这样权限被收回后，事件流最晚会在一次操作之后立即关闭。

**关停**：SSE handler 登记到 `s.conns`。它会监听 `r.Context()` 和服务端的关停信号，收到任一信号就返回，从而保证在 Hub 和存储关闭之前退出。

### 8. 统计与日志缓冲

**统计**：

- `Server` 记录 `startedAt`。
- 消息计数用 `atomic.Int64` 累加。
- 近 24 小时消息数用 24 个按小时的环形桶，以原子方式更新。
- Hub 新增 `stats()`，返回连接数、在线用户 ID 列表、订阅数。
- 存储新增 `Counts()`，返回用户数、频道数、私聊数、各会话 `nextSeq` 之和（即消息总数）。
- 运行时指标用 `runtime.ReadMemStats` 和 `runtime.NumGoroutine`。
- 概览接口把以上数据合并返回。

**日志**：

- 新增 `teeHandler`，把每条记录同时交给原来的 `logfmt` Handler 和一个 500 条的环形缓冲。缓冲用互斥锁保护，因为日志可能来自任何 goroutine。
- 缓冲里存的是不带颜色的已格式化文本。
- 在 `main.go` 组装 logger 时套上这一层。

### 9. 命令行建超级管理员与数据目录锁

**锁**：服务端启动时，在数据目录创建 `server.lock` 并加独占锁，进程退出时由操作系统自动释放，所以异常退出不会留下死锁。

- Unix 用 `syscall.Flock`。
- Windows 用 `LockFileEx`，通过 `golang.org/x/sys/windows`。这个依赖已经被 `golang.org/x/term` 间接引入。

**建超级管理员命令**：`-create-admin <name>` 的执行步骤：

1. 尝试获取同一把锁，失败就报错退出。
2. 用 `term.ReadPassword` 读取两次密码。
3. 打开存储，按以下规则处理，然后写审计记录：
   - 用户不存在：创建用户，设置 `super=true`。
   - 用户已存在：只设置 `super=true`。
4. 关闭存储并退出。

**理由**：直接改文件存储可以避免为命令行单独开一个网络接口。加锁则保证了单写者的前提不被破坏。

### 10. 前端：零依赖单页

前端由三个文件组成：`index.html`、`app.js`、`style.css`。

- 使用原生 ES 模块和 hash 路由，不用框架。
- 所有接口调用经过一个 `api()` 封装，统一加 `X-Requested-With` 头，统一处理 401（回到登录）和 403（提示无权）。

**导航**：由 `/admin/api/me` 返回的 `perms` 决定显示哪些区域。

**会话页**：

- 采用左右两栏，左边是会话列表，右边是消息。
- 向上滚动触发 `before=` 翻页。
- SSE 推来的消息如果属于当前打开的会话，就追加到末尾。

**配色**：沿用 TUI 的 ink 和 paper 两套令牌，换算为 CSS 变量，用 `prefers-color-scheme` 切换。

**测试**：页面本身不做浏览器自动化测试，行为由服务端接口测试覆盖。页面只做手动验收。

### 11. 客户端

新增内容：

- `protocol` 增加错误码、事件名，以及载荷类型 `SanctionInfo{Reason, ExpiresAt}`。
- `AppState` 增加 `MutedUntil int64`，`-1` 表示永久，`0` 表示未禁言。

处理方式：

- **封禁**：处理逻辑接到 `onEvent` 和登录失败分支，复用现有 `session.replaced` 的停止重连和清除令牌流程，最后回到登录舞台，把原因和到期时间写进错误行。
- **禁言**：
  - composer 在禁言期间渲染成只读，显示提示。
  - 用一个定时器在到期时清除 `MutedUntil`。
  - 发送收到 `muted` 时不清空草稿。
- **强制下线**：沿用 `session.replaced`。服务端把事件的 `location` 填为「管理员」，客户端不需要改动。

## Risks / Trade-offs

- **[持有 `chat.view` 的人能看到所有私聊]** → 每次打开会话和每次搜索都写审计，审计只能追加；只有 `accounts.manage` 能查看审计；超级管理员的操作同样会被记录。
- **[搜索仍是顺序扫描，消息量大时一次搜索要分很多批]** → 分片执行，保证聊天不被阻塞。在 design 中记录：消息总量到达百万量级时，应换成 SQLite 存储并建立 FTS 索引。`Storage` 接口不变，所以切换不影响后台。
- **[后台和聊天共用端口，暴露面变大]** → 提供配置开关，可以关闭后台；后台登录与聊天登录共用失败限流；cookie 只发往 `/admin/` 路径；部署文档建议公网部署时用反向代理限制 `/admin/` 的来源 IP。
- **[超级管理员只能用命令行产生，忘了密码就无法登录后台]** → 再次执行 `-create-admin` 不会改密码。所以命令行额外提供 `-reset-password <name>`，同样要求停服后执行。
- **[封禁只靠作废令牌和断开连接]** → 有人可能在作废之前刚好签发出新令牌。为此 `opResume` 每次都会检查封禁状态，这样的令牌也无法使用。
- **[SSE 缓冲满时直接断开，事件会丢失]** → 前端在每次重新连接后重新拉取当前视图的数据，所以界面最终会恢复一致。
- **[内存里只保留 5000 条审计]** → 后台分页超出这个范围后，改为从文件读取，速度较慢，但这是低频操作。

## Migration Plan

- 旧数据不需要迁移：新字段都是可选的，新文件不存在时视为空。
- 部署步骤：停服 → 替换二进制 → 执行 `-create-admin` → 启动服务。
- 回滚：直接换回旧二进制。旧版本会忽略 `users.json` 中多出的字段，也不会读取 `sanctions.json` 和 `audit.jsonl`。代价是回滚之后封禁不再生效。
