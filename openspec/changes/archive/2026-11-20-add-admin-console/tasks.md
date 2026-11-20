# Tasks

## 1. 存储：权限、处分、审计、计数与搜索

- [x] 1.1 `storage.User` 增加 `Perms []string`、`Super bool`（`omitempty`），新增 `SetUserPerms`、`SetUserSuper`、`CreateUser` 支持无安全问题；验证：存储测试覆盖旧 `users.json` 读入后无权限、设置后重开仍在
- [x] 1.2 新增 `sanctions.json` 与 `AddSanction`（同类生效处分标记被取代）、`RevokeSanction`、`SanctionsForUser`、`ActiveSanction(uid, kind, now)`，actor 内维护按用户索引；验证：表驱动测试覆盖生效、到期、永久、撤销、取代、重开后状态一致
- [x] 1.3 新增 `audit.jsonl` 与 `AppendAudit`、`ListAudit(filter, before, limit)`（内存保留最近 5000 条，超出部分从文件读）；接口中不存在删除或修改审计的方法；验证：测试覆盖追加、按操作人/类型/时间过滤、超过 5000 条后的分页、重开后可读
- [x] 1.4 新增 `Counts()`（用户数、频道数、私聊数、消息总数）；已有的 `ListConversations()` 本就包含全部私聊，直接复用；验证：测试断言计数与写入的数据一致
- [x] 1.5 新增 `SearchMessages(filter, cursor)`：按固定会话顺序、会话内 seq 倒序，单次最多扫描 20 000 条、返回 100 条，未尽时返回游标，大小写不敏感、跳过已删除；验证：测试覆盖关键词、作者、会话、时间范围组合，游标续扫不重不漏，扫描上限生效；基准测试记录 20 000 条一次扫描的耗时
- [x] 1.6 令牌：`DeleteSessionTokensForUser` 只删 `session` 用途，新增 `DeleteTokensForUserPurpose`；验证：测试断言删除会话令牌后 `admin` 与 `reset` 令牌仍在

## 2. 协议与处分规则

- [x] 2.1 `pkg/protocol` 新增错误码 `account_banned`、`muted`，事件 `account.banned`、`account.muted`、`account.unmuted`，载荷 `SanctionInfo{reason, expires_at}`，补中英错误文案；验证：协议测试覆盖新错误码不复用已有字符串、载荷序列化往返
- [x] 2.2 `userForToken` 改为按期望用途校验，用途不符视为不存在；验证：服务端测试断言后台令牌恢复聊天会话返回 `unauthorized`
- [x] 2.3 抽出 `notifyAndClose(uid, frame)`，`notifyDisplaced` 改用它；验证：现有顶号测试全部通过
- [x] 2.4 封禁检查接入 `opLogin`（密码通过之后）、`opResume`、找回密码三步（返回 `cannot_recover`）；验证：服务端测试覆盖被封账号正确密码返回 `account_banned` 与原因、错误密码返回 `invalid_credentials`、恢复被拒、找回返回 `cannot_recover`、到期后可登录
- [x] 2.5 禁言检查接入 `opMessageSend`、`opMessageEdit`、`opChannelCreate`；验证：服务端测试覆盖禁言时发送/编辑/建频道被拒且其他成员不收事件、删除自己的消息与读历史不受影响、到期后可发送
- [x] 2.6 新增 `moderation.go`：封禁（写记录 → 作废 session 与 admin 令牌 → 推 `account.banned` → 关连接 → 关该用户的后台事件流）、禁言（推 `account.muted`）、撤销（禁言推 `account.unmuted`）、重置密码、强制下线（推 `session.replaced`，来源为管理员）；每个操作写审计；验证：服务端测试用两条真实 WS 连接断言事件先于关闭送达、令牌失效、审计记录字段完整且不含密码
- [x] 2.7 `user.list` 等聊天请求不返回权限与处分字段；验证：测试断言用户列表 JSON 中没有 `perms`、`super`
- [x] 2.8 更新 `docs/server.md` 的事件与错误码表；验证：文档列出两个新错误码与三个新事件

## 3. 后台认证与接口

- [x] 3.1 新增 `admin_perms.go`：权限常量、`effectivePerms(user)`、纯函数 `canGrant(actor, target, newPerms)`；验证：表驱动测试穷举改自己、动超管、授予未持有的权限、非超管动 `accounts.manage`
- [x] 3.2 新增 `admin_auth.go`：`POST /admin/api/login|logout`（复用 Argon2 校验与登录限流，签发 12 小时 `admin` 令牌，写 HttpOnly/SameSite=Strict/Path=/admin/ cookie，TLS 或可信代理 https 时加 Secure）、`adminAuth(perm)` 中间件（每请求查令牌、用户、封禁、权限）、非 GET 要求 `X-Requested-With` 与 JSON；验证：`httptest` 测试覆盖普通用户登录 403、错误密码计入限流、过期 401、退出后 401、权限收回后下一请求 403、缺少头的 POST 被拒、聊天令牌访问后台 401、登录后台不顶掉聊天连接
- [x] 3.3 新增 `admin_api.go`：`/me`、`/stats`、`/users`、`/users/{id}`、`/users/{id}/ban|mute|revoke|reset-password|kick|perms`、`POST /accounts`、`/conversations`、`/conversations/{id}/messages?before=`、`/search`、`/audit`，每个按 spec 所需权限鉴权，查看会话与搜索写审计；验证：每个接口一组测试覆盖有权成功与无权 403，处分接口拒绝空原因、拒绝作用于超管，建号校验用户名与密码规则
- [x] 3.4 统计：服务端版本、在线连接的客户端版本与版本分布（`hub.stats()` 读 `Conn.clientVersion`，空为未知）、`Server.startedAt`、原子消息计数与 24 小时按小时环形桶、`hub.stats()`、内存与协程数，汇总进 `/stats`，`/users` 与 `/users/{id}` 返回在线连接与会话令牌上的客户端版本；验证：测试发 N 条消息后 `/stats` 的消息数与 24 小时计数正确，不同版本的客户端连接后版本分布与用户版本正确
- [x] 3.5 日志：`teeHandler` 把每条日志同时写入原 Handler 与 500 条环形缓冲，`main.go` 组装时套上，`/stats` 返回最近日志；验证：测试写 600 条后缓冲只剩最新 500 条且顺序正确，格式与终端输出一致（不含颜色）
- [x] 3.6 `Config` 新增 `admin_enabled`（默认 true）与环境变量 `LINKCHAT_ADMIN_ENABLED`，关闭时 `/admin/` 全部 404 并在启动日志说明；验证：配置测试与路由测试

## 4. 实时事件流

- [x] 4.1 Hub 新增后台订阅者集合与 `closeAdminSubs(uid)`；`s.adminEmit(ev)` 在消息发送/编辑/删除、上下线、注册、登录、处分变化处调用；在 Hub 外按订阅者权限裁剪（无 `chat.view` 只收计数），非阻塞投递，缓冲满即关闭订阅者；验证：服务端测试断言有 `chat.view` 收到正文、只有 `stats.view` 只收计数、慢订阅者被关闭而聊天广播不受影响
- [x] 4.2 `GET /admin/api/events`（SSE）：登记到 `s.conns`，每 15 秒心跳并重新鉴权，权限收回或封禁时关闭，服务端关停时返回；验证：测试覆盖收到事件、收回权限后流结束、`Shutdown` 在宽限期内完成且无 goroutine 泄漏

## 5. 前端页面

- [x] 5.1 新增 `pkg/server/adminweb/`（`index.html`、`app.js`、`style.css`）并用 `go:embed` 挂到 `/admin/`：登录表单、`api()` 封装（统一加头、401 回登录、403 提示）、按 `/me` 权限渲染导航、ink/paper 配色随系统深浅色；验证：`go test` 断言 `/admin/` 返回页面且静态资源可取，手动在浏览器登录
- [x] 5.2 概览页：统计卡片（含服务端版本）、在线用户列表（含客户端版本）、版本分布、最近日志，经 SSE 实时更新在线与计数；验证：手动开两个 TUI 客户端上下线，概览不刷新即变化
- [x] 5.3 会话页（左列会话含全部私聊、右列消息向上翻页、SSE 追加当前会话新消息、编辑与删除标记）与搜索页（关键词、作者、会话、时间范围，「继续搜索」使用游标）；验证：手动在 TUI 发消息后会话页实时出现，搜索结果与条件一致
- [x] 5.4 用户页（列表含在线与处分状态、详情含会话来源与处分历史、封禁/禁言表单：原因必填、时长 1 小时/1 天/7 天/30 天/自定义/永久、撤销、重置密码、强制下线）；账号页（建号、勾选权限，无法授予的项禁用并说明原因）；审计页（过滤与分页）；验证：手动按 spec 场景逐项操作，结果与服务端测试一致

## 6. 命令行与数据目录锁

- [x] 6.1 数据目录锁：服务端启动时对 `server.lock` 加独占锁（Unix `flock`，Windows `LockFileEx`），已被占用时拒绝启动；`go.mod` 将 `golang.org/x/sys`、`golang.org/x/term` 改为直接依赖；验证：测试中同一目录二次加锁失败，释放后可再加锁；进程被杀后可重启（手动）
- [x] 6.2 `-create-admin <name>` 与 `-reset-password <name>`：取锁失败即报错退出，无回显读两次密码并校验强度，创建或提升超管（不改已有密码）/ 重置密码并作废全部令牌，写审计（操作人 `cli`），完成后退出；读密码的部分可注入以便测试；验证：测试覆盖新建、提升不改密码、两次不一致、账号不存在、服务端运行中拒绝执行
- [x] 6.3 更新 `docs/server.md`：后台入口与开关、建超管与重置密码命令、权限说明、审计、公网部署建议用反向代理限制 `/admin/`；验证：按文档步骤从零执行一遍能登录后台

## 7. 客户端

- [x] 7.1 封禁：登录/恢复收到 `account_banned`、在线收到 `account.banned` 时停止重连、清除该账号令牌、回到登录舞台并在错误行显示原因与解封时间（永久显示「永久」），补中英文案；验证：e2e 测试（真实服务端）在线被封后回到舞台并显示原因，登录时被拒显示原因且保留用户名，文案两种语言下不超出面板
- [x] 7.2 禁言：`AppState.MutedUntil`，收到 `account.muted` 或发送收到 `muted` 时输入区只读并显示「禁言至 …」，`account.unmuted` 或到期定时器恢复，发送被拒保留草稿；验证：视图快照测试与 e2e 测试覆盖进入禁言、撤销恢复、到期恢复、草稿保留
- [x] 7.3 更新 `docs/client.md`：被封禁与被禁言时的界面；验证：文档包含两种状态的说明

## 8. 整合验收

- [x] 8.1 端到端手动验收：`-create-admin` 建超管 → 浏览器登录 `/admin/` → 两个 TUI 客户端互聊、后台实时可见 → 建一个只有 `stats.view` 的账号确认只看到概览 → 封禁在线用户确认其客户端立即掉线并显示原因 → 禁言确认输入区只读 → 审计页能看到以上全部操作；验证：逐项通过
- [ ] 8.2 `gofmt`、`go vet`、`go test ./...`、`go test -race ./pkg/server/... ./pkg/storage/...`、`openspec validate add-admin-console --strict` 全部通过

## 9. 版本号

- [x] 9.1 按版本规则把根目录 `VERSION` 从 `1.0.0` 改为 `2.0.0`；验证：`go test ./pkg/version/...` 通过，两个二进制 `-version` 输出 `2.0.0`

## Workflow follow-up

- 用户确认后归档：`openspec archive add-admin-console`，再 `openspec validate --all --strict`。
