# Design

## Context

- 客户端设置现在都在按实例划分的本机配置文件 `Config` 里（`pkg/app/app.go`）：`Theme`、`Language`、`MotionOff` 和聊天、消息样式各字段，与 `RememberSession`、`Server`、`Accounts` 混在同一个结构体中。配置按实例而不是按账号划分，同一实例下切换账号时会共用同一份设置。
- 服务端存储是领域形状的接口 `storage.Storage`，JSON 后端用单个 actor goroutine 持有全部数据，每类数据一个文件（`users.json`、`tokens.json`……）。
- 请求按 `op` 字符串在 `Server.dispatch` 中分派，`requireAuth` 取当前连接的用户；`Hub.connectionsForUser(userID, keep)` 能取出同一用户除自身以外的连接，`Conn.enqueue` 推帧。
- 服务端对未知 `op` 返回 `invalid_argument`。
- 同一账号只允许一个会话（`login-session`），所以「其他在线连接」目前几乎不存在。

## Goals / Non-Goals

**Goals:**
- 设置的取值规则（候选、默认、非法回退）只定义一次，客户端与服务端共用，避免两边对「合法值」意见不一。
- 登录到界面可用之间不新增等待：下载在后台进行，到达后再切换。
- 旧客户端、旧服务端、旧配置文件都能继续工作。

**Non-Goals:**
- 不做冲突合并或版本号：后写的覆盖先写的（同一账号同时只有一个会话，冲突几乎不会发生）。
- 不同步本机设置（记住登录、服务器地址、凭据存储、最近账号）。
- 不改变单会话约束。

## Decisions

### 1. 设置的定义放在 `pkg/protocol`

新增 `protocol.Settings`（`map[string]string` 的具名类型）与一张设置表：每个键的候选值与默认值，以及 `Validate()`（未知键或非法值返回错误）和 `Normalize()`（丢弃未知键和非法值，补默认）。客户端的 `Config.Layout()`、设置页候选值，以及服务端的保存校验，都基于这一张表。

- 备选：服务端存不透明的 JSON，不做校验。这样实现最简单，但一个写坏了的客户端会把错误数据推给该账号的所有设备；而且规格要求非法值被拒绝。
- 备选：两边各写一份。会漂移，否决。
- 颜色候选目前定义在 `views.MessageColors`，挪到 `protocol` 表里，`views` 改为从表中引用，避免 `protocol` 反向依赖 `views`。

### 2. 键名与取值沿用现有配置字段

键名采用规格中的 `theme`、`motion`、`bubble_width` 等，取值与现有配置文件中的字符串一致（`"paper"`、`"compact"`、`"12h"`、`"hide"`、`"amber"`）。`motion` 的取值是 `"on"`/`"off"`，对应现有的 `MotionOff` 布尔；`bubble_width` 是 `"60"`/`"70"`/`"80"`。这样迁移只是一次字段搬运，不需要映射表。

### 3. 服务端存储：每个用户一条记录，单独一个文件

`storage.Storage` 新增 `UserSettings(userID) (map[string]string, bool, error)` 和 `MergeUserSettings(userID, patch) (map[string]string, error)`。合并在 actor 内完成，读、改、写是一个原子步骤。JSON 后端新增 `settings.json`（`userID → map`）。

- 备选：把设置字段加进 `User` 结构。会让每次改设置都重写包含密码哈希的 `users.json`，读设置也会把凭据加载进内存路径；而且以后换数据库时，分表更自然。否决。

### 4. 推送：保存成功后发给同一用户的其他连接

`opSettingsPut` 保存成功后，先回复请求，再用 `connectionsForUser(uid, c)` 取出其他连接并推送 `settings.updated`。发起方被 `keep` 排除，符合「发起方不收事件」的规定。

### 5. 客户端配置拆成本机部分和按账号的缓存

`Config` 中账号设置的字段，改为一个按用户名索引的缓存 `AccountSettings map[string]protocol.Settings`，同时保留 `LastAccount`（即 `Accounts[0]`，已存在）。当前生效的账号设置由「当前账号的缓存 → 默认值」决定：

- 登录前：用 `Accounts[0]` 的缓存。
- 从账号列表或登录面板选中某账号、开始登录时：切换到该账号的缓存。这在下载之前完成，所以不会先显示上一个账号的样子。
- 登录成功：后台 `settings.get`。`exists=true` 时用服务器的值覆盖缓存并应用；`exists=false` 时把当前缓存上传；失败或超时就保持缓存。

缓存仍写在本机配置文件里，与其他字段一起原子保存。

- 备选：每个账号一个单独的文件。只会增加文件数量，没有其他收益，否决。
- 用户名作为键：同一实例的账号由服务器地址和用户名确定，最近账号列表本来就用用户名。服务器地址变化（连到另一个服务器上的同名账号）时，缓存可能张冠李戴，但登录后的下载会纠正它；这个代价可以接受，记在风险里。

### 6. 应用设置走同一条路径

新增 `App.applyAccountSettings(s protocol.Settings, from source)`：更新主题令牌、语言、动画开关和消息布局，然后标记重绘。登录下载、推送事件、设置页确认都调用它；设置页确认在此之后再上传。语言和动画已有的 `toggleLanguage` / `toggleMotion` 改为调用它，避免两条路径行为不一致。

推送或下载在用户处于选择模式时到达：先记在 `pendingSettings` 里，等 `ActCommit` / `ActCancel` 后再应用。如果用户确认的正是同一项，以本地确认的值为准，确认后的上传会再覆盖服务器。

### 7. 上传失败与旧服务端

- 上传使用 `tr.Call(..., 10s)`，在 goroutine 里执行。失败时用 `setStatus(i18n.SettingsNotSynced, true)` 提示；不自动重试，下次确认或下次登录时自然会对齐。
- `settings.get` 返回 `invalid_argument` 时，记 `a.syncUnsupported = true`，本次连接内不再发起任何设置请求，也不提示失败。重新连接时重新探测。

### 8. 迁移

`LoadConfig` 读到旧格式（顶层有 `theme`、`language`、`bubble_width` 等字段，且 `AccountSettings` 为空）时，把这些值迁入 `AccountSettings[Accounts[0]]`（`Accounts` 为空时用 `Username`；两者都为空时存为「无账号」默认缓存，下一次登录时归到该账号名下），并清掉顶层字段。这样首次登录发现服务器上没有记录时，上传的就是用户原来的设置。

## Risks / Trade-offs

- [首次上传与另一台机器竞争：两台都还没上传过的机器先后登录，先上传的那份会被后一台读到] → 这是规格要求的行为（服务器上有就以服务器为准），后一台只会看到前一台的设置，不会出现两份互相覆盖。
- [按用户名缓存，连接到另一个服务器上的同名账号时，下载前会短暂显示错误的缓存] → 下载后立即纠正；需要的话，以后可以用「服务器地址 + 用户名」作为键，不影响协议。
- [语言切换出现在登录之后，入场动画期间界面文字会变化] → 只有缓存与服务器不一致时才会发生（换机器后的首次登录），可以接受；入场动画只覆盖已画出的内容，不会造成错位。
- [服务端校验表与客户端版本不一致：新版客户端多了一个候选值，旧服务端会拒绝整个保存] → 校验表在同一个包中，服务端随版本一起发布；跨版本时客户端得到 `invalid_argument`，会提示未同步，本地仍然生效，不会丢失设置。

## Migration Plan

1. 先部署服务端：旧客户端不发新请求，没有影响。
2. 再发布客户端：首次运行时迁移本机配置；首次登录时，服务器上没有记录，就上传迁移后的设置。
3. 回滚：客户端回退到旧版本时，旧版本读不到顶层设置字段，会回到默认值。为此，迁移时在顶层保留一份旧字段的副本（只写不读），旧版本照样能读到最后一次的值；服务端回滚不影响旧客户端。
