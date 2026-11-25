# Design

## Context

动机见 `proposal.md`。与本设计相关的现状：

- 存储是 SQLite（`pkg/storage`，3.0.0 引入），`Storage` 接口按领域划分，所有表在 `Open` 时 `CREATE IF NOT EXISTS`；项目未上线，不需要数据迁移。
- 服务端请求按 `op` 在 `dispatch` 里分派；每连接有统一的请求频率预算；禁言检查走 `failIfMuted`；事件经 `hub.connectionsForUser` / `broadcastToUsers` 推送；审计经 `s.audit`。
- 客户端左侧底部入口是 `client.Entries`，每个入口对应一个 `client.Page`，页面在 `renderPage` / `handleRightKey` 里分派；入口页面已有"选中即显示、→ 进入并折叠列表"的统一机制。
- 消息颜色已有六个命名色（cyan、blue、green、amber、rose、violet），亮暗主题都可读，单色终端回退为灰。
- 管理后台是无构建步骤的纯 DOM 页面，区域在 `SECTIONS` 表里按权限注册。

## Goals / Non-Goals

**Goals:**
- 一个可见性判断函数决定"谁能看到哪条动态、哪个赞、哪条评论"，所有读路径和推送路径都走它。
- 好友、主页、动态都复用现有的请求分派、禁言、审计、事件推送机制，不另起一套。
- 客户端新增页面完全走现有的入口页面机制。

**Non-Goals:**
- 图片、视频、链接预览：终端里显示不了。
- 动态的编辑：删除后重发即可，不做编辑历史。
- 分组可见（"部分好友可见"）：只做"所有好友 / 仅自己"。
- 好友备注名。

## Decisions

### 1. 数据模型

```
user_profiles(user_id PK, nickname, bio, avatar, color, updated_at)
friendships(user_a, user_b, created_at, PK(user_a, user_b))   -- user_a < user_b，一行表示一对
friend_requests(id PK, from_id, to_id, note, created_at, UNIQUE(from_id, to_id))
blocks(blocker_id, blocked_id, created_at, PK(blocker_id, blocked_id))
moments(pk INTEGER PK, id UNIQUE, author_id, body, visibility, created_at)
  INDEX moments_author_time(author_id, created_at DESC, id DESC)
  INDEX moments_time(created_at DESC, id DESC)
moment_likes(moment_id, user_id, created_at, PK(moment_id, user_id))
moment_comments(id PK, moment_id, author_id, reply_to, reply_to_user, body, created_at)
  INDEX comments_moment(moment_id, created_at)
moment_notifications(id INTEGER PK, user_id, kind, actor_id, moment_id, comment_id, body, created_at, read)
  INDEX notif_user(user_id, id DESC)
moment_seen(user_id PK, seen_at)
```

**理由**：
- 好友一对只存一行（较小的 ID 在前），"是否好友"是一次主键查找，删除是一条 DELETE，不会出现单边残留。
- 资料单独成表而不是加在 `users` 上：`users` 的列与认证相关，资料是展示用的，变更频率与权限都不同；缺行即默认资料，不必给老账号补行。
- 通知存正文快照（评论内容），评论被删后通知仍可读，与常见 IM 一致。
- 删除动态时在同一事务里删掉点赞、评论与指向它的通知。

**替代方案**：好友存双向两行——查询"我的好友"略简单，但每次增删要维护两行一致，得不偿失。

### 2. 可见性集中在一处

服务端 `visibility.go` 提供：

- `canSeeMoment(viewer, m)`：作者本人；或 `visibility=friends` 且 viewer 与作者是好友，且双方没有拉黑。
- `filterInteractions(viewer, m, likes, comments)`：作者看到全部；其他人只看到自己的与其好友的。回复对象不可见时把 `reply_to_user` 置空。

每个查询结果先取当前 viewer 的好友集合（一次 SQL），再在内存里过滤，避免每条动态多次查询。时间线 SQL 直接用 `author_id IN (自己 + 好友)` 加 visibility 条件，只在数据库层面过滤动态本身；互动过滤在内存里做。

**理由**：一处实现、一处测试；推送路径（`moment.updated` 发给谁、发什么）调用同一组函数为每个接收者单独生成载荷，因为不同接收者看到的评论不同。

### 3. 推送：按接收者生成载荷

`moment.new` / `moment.updated` / `moment.deleted` 的接收者是"作者 + 作者的在线好友"中 `canSeeMoment` 为真的人；对每人调用 `filterInteractions` 生成各自的载荷。在线好友数量有限（聊天规模），逐人生成的成本可以接受。

**替代方案**：只推"某条动态变了"，让客户端重拉——多一次往返，且离线期间的批量变更会造成重拉风暴；按人生成更直接。

### 4. 头像与昵称在服务端补全

`protocol.User` 增加 `nickname/avatar/color`，`protocol.Message` 增加 `author_avatar/author_color`。服务端读资料时缺行即返回默认值：头像为用户名首字符（大写），颜色为 `colors[fnv32(username) % 6]`。默认值不落库，用户修改后才写行。

**理由**：客户端不必知道默认规则；所有返回用户的接口一致。消息只带头像不带昵称，昵称在聊天里不显示（聊天仍按用户名，避免冒名混淆），只在朋友圈与主页显示。

### 5. 头像字符校验

头像字符必须是**一个字形簇**（用 `uniseg` 判断，项目已间接依赖 `rivo/uniseg`），且 `layout.StringWidth` ≤ 2，不能是控制字符或空白。颜色必须是六个命名色之一。服务端校验用 `go-runewidth`（已是依赖），与客户端宽度规则一致。

### 6. 频率限制

复用 `rateLimiter`，按**用户 ID**（不是连接）计：`friendLimit` 每小时 30、`postLimit` 每小时 20、`interactLimit` 每分钟 30（点赞与评论合计）。按用户计是因为同一账号可以多次重连绕过连接级预算。阈值写进 `Config`，有默认值，支持配置覆盖。

### 7. 朋友圈未读

- 新动态数：`SELECT count(*) FROM moments WHERE author_id IN (好友) AND visibility='friends' AND created_at > seen_at`。
- 未读通知数：`moment_notifications WHERE user_id=? AND read=0`。
- 待处理申请数：`friend_requests WHERE to_id=?`。

客户端登录后调一次 `moment.unread`，之后靠事件累加；进入时间线第一页时服务端更新 `seen_at`，客户端把新动态计数清零。

### 8. 客户端页面结构

- 新增 `client.EntryMoments`，排在 `Entries` 最前；新增 `client.PageMoments` 与 `client.PageProfile`。
- `PageProfile` 不对应任何入口行：从朋友圈或好友列表打开，`ui.ProfileBack` 记住来处，Esc 返回。列表光标停在"朋友圈"入口上。
- 朋友圈页面状态 `MomentsModel`：当前标签、各标签的选中项与滚动、时间线数据与游标、通知、好友数据、输入框状态（发动态、评论、加好友共用一个输入模式：`Compose{Kind, Target, Text, Visibility}`）。
- 视图层只做"模型 → 单元格"：`views.MomentsPage`、`views.ProfilePage`，各自把条目渲染成行（动态按宽度折行，点赞与评论缩进），选中项高亮整块，滚动保证选中项可见。
- 网络调用在 `app/moments.go`，沿用 `a.tr.Call` + goroutine + `a.mu` + `markDirty` 的既有模式。
- 新增快捷键 Alt+P 打开朋友圈；朋友圈内单字母快捷键只在没有输入框打开时生效。

### 9. 头像渲染位置

- 会话列表：私聊行在名字前画两格头像（宽 1 的字符补空格）。频道行不画。
- 消息气泡：作者名前画头像。需要在 `client.Conversation`/消息里带上头像；消息已带 `author_avatar/author_color`，私聊对端的头像从 `user.list` 结果缓存（`AppState.Profiles map[userID]protocol.User`），`profile.updated` 事件更新它。
- 颜色映射复用 `messageColorHex`；单色终端只画字符。

### 10. 管理后台

- 新接口：`GET /admin/api/moments?author=&cursor_at=&cursor_id=`（`chat.view`），`POST /admin/api/moments/{id}/delete` 与 `POST /admin/api/comments/{id}/delete`（`users.moderate`）。
- 管理员视角不过滤可见性：看到全部动态与全部评论。
- 删除后对能看到它的在线用户推送 `moment.deleted` / `moment.updated`，审计动作为 `delete_moment` / `delete_comment`，detail 含作者用户名与正文。
- 页面新增 `SECTIONS` 项"动态"。

## Risks / Trade-offs

- **互动可见性按好友集合在内存过滤** → 一条动态的评论多时，每个查看者都要遍历一次。聊天规模（单条动态几十条评论）没问题；超过时再改为 SQL 过滤。
- **推送按人生成载荷** → 一个人有上百在线好友时，一次评论要生成上百份 JSON。可接受；如成瓶颈，可以按"好友集合与作者好友集合的交集"分组复用。
- **删除好友后对方本地仍缓存旧动态** → 服务端在删除/拉黑时向双方推送 `friend.removed`，客户端收到后从时间线移除对方的动态并刷新好友列表，防止"删了还看得到"。
- **昵称可冒充他人** → 聊天里仍显示用户名，昵称只在朋友圈和主页显示，且主页总是同时显示 `@用户名`。
- **客户端界面体量大** → 朋友圈页面是本变更最大的一块；视图拆成每个标签一个渲染函数，并为每个标签写渲染快照测试与按键测试。

## Migration Plan

项目未上线，新表在 `Open` 时创建，无迁移。客户端连旧服务端（3.x）时新请求返回 `invalid_argument`：朋友圈页面显示"服务器版本过旧，不支持朋友圈"，其余功能不受影响。

## Open Questions

（无）
