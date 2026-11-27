# chat-protocol Specification

## Purpose
定义客户端与服务端之间的 WebSocket 通信契约：帧结构、版本协商、请求/响应/事件三类消息、错误码、心跳与超时。任何客户端实现只要遵守本契约即可与服务端互通，不依赖具体语言或库。

## Requirements

### Requirement: 协议版本协商

每一端 MUST 在连接建立后的第一条消息中声明其协议版本。服务端 SHALL 在能力不匹配时明确拒绝，而不是降级为不可预测的行为。

#### Scenario: 版本一致时正常握手
- **WHEN** 客户端发送 `hello` 帧并携带 `version: 1`
- **THEN** 服务端返回 `hello-ack` 帧，其中 `version` 为服务端支持的版本，连接进入已建立状态

#### Scenario: 客户端版本过高
- **WHEN** 客户端发送的 `version` 大于服务端支持的最高版本
- **THEN** 服务端返回错误码 `protocol_version_unsupported` 并关闭连接，错误体包含服务端支持的版本范围

#### Scenario: 首个帧不是 hello
- **WHEN** 连接建立后客户端发送的第一个帧类型不是 `hello`
- **THEN** 服务端返回错误码 `protocol_violation` 并立即关闭连接

### Requirement: 帧信封结构

所有帧 MUST 是一个 JSON 对象，包含 `type`、`id`（可选）与 `payload` 三个字段。未知的额外字段 MUST 被忽略而非导致解析失败，以便未来扩展不破坏旧客户端。

#### Scenario: 合法帧被解析
- **WHEN** 服务端收到 `{"type":"ping","id":"7","payload":{}}`
- **THEN** 服务端成功解析该帧并处理 `ping`

#### Scenario: 帧中带有未知字段
- **WHEN** 客户端在 `payload` 中加入服务端不认识的字段 `futureField`
- **THEN** 服务端忽略该字段并正常处理帧，不返回解析错误

#### Scenario: 帧不是合法 JSON
- **WHEN** 服务端收到无法解析的字节序列
- **THEN** 服务端返回错误码 `malformed_frame`，连接保持打开，连续违规达到阈值时关闭连接

### Requirement: 请求与响应关联

客户端发起的请求 MUST 携带唯一 `id`，服务端返回的响应 MUST 回带同一个 `id`。服务端主动推送的事件 MUST NOT 携带请求 `id`。心跳请求与其他请求遵循同一组关联规则。

#### Scenario: 请求得到对应响应
- **WHEN** 客户端发送 `id` 为 `a1` 的 `send-message` 请求
- **THEN** 服务端返回 `id` 为 `a1` 的响应，且该响应只有一条

#### Scenario: 事件不带请求 id
- **WHEN** 其他用户向同一频道发送消息
- **THEN** 客户端收到 `type` 为 `message` 的事件帧，其中不存在 `id` 字段

#### Scenario: 重复的请求 id
- **WHEN** 客户端在尚未收到响应前用相同 `id` 发出第二个请求
- **THEN** 服务端返回错误码 `duplicate_request_id` 且不重复执行该请求的副作用

#### Scenario: 心跳请求得到关联响应
- **WHEN** 客户端发送带 `id` 的心跳请求
- **THEN** 服务端返回回带同一 `id` 的响应，客户端据此确认链路存活

### Requirement: 错误表示

所有失败响应 MUST 包含机器可读的 `code` 与人类可读的 `message`。错误码集合 SHALL 稳定，新增错误码不得复用已有字符串。

#### Scenario: 参数校验失败
- **WHEN** 客户端发送的消息体为空字符串
- **THEN** 服务端返回 `code` 为 `invalid_argument` 的响应，且消息未被广播

#### Scenario: 未认证访问
- **WHEN** 尚未登录的连接发送 `send-message` 请求
- **THEN** 服务端返回 `code` 为 `unauthorized` 的响应

#### Scenario: 权限不足
- **WHEN** 非频道成员发送向该频道发消息的请求
- **THEN** 服务端返回 `code` 为 `forbidden` 的响应

### Requirement: 频道可见性与发现

创建频道请求可携带 `visibility`（`public` 或 `private`）及简介字段 `topic`。客户端创建私有频道时 MUST 使用 `channel.create_private`，使旧服务端能够拒绝该请求。普通 `channel.create` 缺省可见性为 `public`。频道列表和频道响应 SHALL 返回频道的可见性及已填写的简介。

支持编辑频道的服务端 SHALL 在握手响应中携带 `channel_update: true`。旧服务端拒绝编辑请求时，客户端提示更新并重启服务端，保留未保存的表单。

#### Scenario: 在线发现公开频道
- **WHEN** 有人创建公开频道
- **THEN** 服务端向已认证连接广播 `channel.created`，事件数据携带 `conversation_id`，客户端重新获取 `channel.list`

#### Scenario: 公开频道删除
- **WHEN** 创建者删除公开频道
- **THEN** 所有已认证连接收到 `channel.gone`，频道从已加入或可加入列表中消失

#### Scenario: 创建者更新频道
- **WHEN** 创建者通过 `channel.update` 提交 `conversation_id`、`name`、`topic` 和 `visibility`
- **THEN** 服务端返回最新频道元数据。可见性保持或转为私有时，有权查看的在线用户收到 `channel.updated` 并刷新会话列表。公开转私有时，其他用户收到 `channel.gone`。私有转公开时，所有已认证连接收到 `channel.created`。

### Requirement: 心跳与超时

连接 MUST 通过双向心跳检测存活，且客户端 MUST 主动发送心跳使空闲连接保持有效。任何一端在连续两个心跳周期内未收到对端任何帧，MUST 视为断线并关闭连接。已认证客户端 SHALL 在切换频道聊天界面时通过心跳的可选字段 `viewed_channel` 上报所查看的频道；空字符串表示当前没有查看频道。

#### Scenario: 服务端检测到失联客户端
- **WHEN** 客户端在 `heartbeat_interval * 2` 内未发送任何帧
- **THEN** 服务端关闭该连接，并像正常断开一样更新其在线状态

#### Scenario: 客户端检测到服务端失联
- **WHEN** 客户端在 `heartbeat_interval * 2` 内未收到服务端任何帧
- **THEN** 客户端关闭本地连接并进入重连流程

#### Scenario: 心跳不打断空闲会话
- **WHEN** 用户 10 分钟未发送任何消息但客户端持续心跳
- **THEN** 连接保持建立，服务端不将其判定为离线

#### Scenario: 客户端主动心跳保持空闲连接
- **WHEN** 用户长时间不操作键盘、也未收发任何消息
- **THEN** 客户端按心跳周期主动发送心跳请求，服务端回以心跳响应，连接不被读超时关闭

#### Scenario: 心跳帧重置读超时
- **WHEN** 服务端收到任意合法帧（含心跳请求）
- **THEN** 该连接的读超时从此刻重新计算

#### Scenario: 上报频道界面与旧客户端兼容
- **WHEN** 已登录用户打开已加入的频道，随后退出频道界面，或者旧客户端发送未包含该字段的心跳
- **THEN** 新客户端可先上报频道再清除查看状态，服务端照常响应旧客户端的心跳

### Requirement: 帧大小与速率上限

服务端 MUST 限制单帧大小与单位时间内的帧数量，超限时拒绝并说明原因，避免单个连接耗尽服务端资源。

#### Scenario: 超大帧被拒绝
- **WHEN** 客户端发送的帧超过配置的 `max_frame_bytes`
- **THEN** 服务端返回 `code` 为 `frame_too_large` 的响应且不处理该帧内容

#### Scenario: 突发请求被限流
- **WHEN** 同一连接在 1 秒内发出的请求数超过配置的 `requests_per_second`
- **THEN** 服务端对超出部分返回 `code` 为 `rate_limited` 的响应，并附带建议的重试等待毫秒数

### Requirement: 会话被顶掉事件

服务端 SHALL 在被顶掉的连接关闭前推送 `session.replaced` 事件，载荷 MUST 包含账号名、顶号方来源地址与时间戳，并可包含设备名。该事件 MUST NOT 携带请求 `id`。

#### Scenario: 事件字段完整
- **WHEN** 某会话被顶掉
- **THEN** 事件载荷包含 `username`、`location`、`at` 三个字段，`device` 在客户端上报时出现

#### Scenario: 事件在断开之前送达
- **WHEN** 服务端决定断开被顶掉的连接
- **THEN** 事件先写入该连接，随后才关闭，客户端不会错过通知

#### Scenario: 旧客户端忽略未知事件
- **WHEN** 未实现该事件的客户端收到它
- **THEN** 客户端忽略该事件而不报错，连接仍会随后关闭

### Requirement: 账号设置的请求与事件

协议 SHALL 提供读取与保存当前账号设置的两个请求，以及一个设置已更新事件：

- `settings.get`：无参数。响应载荷为 `{"settings": <设置对象>, "exists": <布尔>}`；该账号从未保存过设置时 `exists` 为 `false`，`settings` 为空对象。
- `settings.put`：载荷为 `{"settings": <设置对象>}`，只包含要修改的项。成功时响应载荷为保存后的完整 `{"settings": <设置对象>}`。
- `settings.updated` 事件：载荷为保存后的完整 `{"settings": <设置对象>}`，不携带请求 `id`。

未认证的连接发出这两个请求时 MUST 返回 `unauthorized`。

#### Scenario: 读取尚未保存的设置
- **WHEN** 新注册的账号发送 `settings.get`
- **THEN** 响应中 `exists` 为 `false`，`settings` 为空对象

#### Scenario: 保存后读取
- **WHEN** 客户端发送 `settings.put`，载荷为 `{"settings": {"theme": "paper"}}`
- **THEN** 响应载荷中 `settings.theme` 为 `"paper"`，之后的 `settings.get` 返回 `exists` 为 `true` 且主题相同

#### Scenario: 未登录请求设置
- **WHEN** 尚未登录的连接发送 `settings.get`
- **THEN** 服务端返回 `unauthorized`

#### Scenario: 旧客户端忽略设置事件
- **WHEN** 未实现 `settings.updated` 的客户端收到该事件
- **THEN** 客户端忽略该事件而不报错，连接保持

### Requirement: 设置对象的键与校验

设置对象的键 MUST 为以下之一：`theme`、`motion`、`language`、`bubble_width`、`density`、`time_format`、`msg_border`、`msg_time`、`self_text`、`other_text`、`self_border`、`other_border`、`plaza`、`wheel_key`，值均为字符串。`self_text`、`other_text`、`self_border`、`other_border` 取 `default`/`red`/`orange`/`yellow`/`amber`/`lime`/`green`/`teal`/`cyan`/`sky`/`blue`/`violet`/`pink`/`rose`/`gray`；`plaza` 取 `on`/`off`；`wheel_key` 取 `f1`（默认）/`ctrl_space`/`ctrl_g`/`ctrl_k`/`ctrl_o`。`settings.put` 中出现未知的键或不在候选内的值时，服务端 MUST 返回 `invalid_argument`，且不保存任何项。

#### Scenario: 未知的键
- **WHEN** `settings.put` 载荷包含键 `font_size`
- **THEN** 服务端返回 `invalid_argument`，不保存任何项

#### Scenario: 不在候选内的值
- **WHEN** `settings.put` 载荷中 `bubble_width` 为 `"55"`
- **THEN** 服务端返回 `invalid_argument`，不保存任何项

#### Scenario: 保存快捷转盘按键
- **WHEN** `settings.put` 载荷为 `{"settings": {"wheel_key": "f1"}}`
- **THEN** 服务端保存成功，`settings.get` 返回的 `wheel_key` 为 `"f1"`

### Requirement: 服务端不支持设置请求时的识别

服务端不认识 `settings.get`、`settings.put` 时，返回 `invalid_argument`（旧服务端对未知请求的既有行为）。因为 `settings.get` 没有参数，不可能有其他参数错误，客户端 MUST 把 `settings.get` 得到的 `invalid_argument` 视为服务端不支持设置同步。

#### Scenario: 旧服务端的响应
- **WHEN** 客户端向没有设置请求的旧服务端发送 `settings.get`
- **THEN** 响应的错误码为 `invalid_argument`，客户端据此判定服务端不支持设置同步

### Requirement: 握手交换发布版本

客户端 SHALL 在 `hello` 帧的 `client` 字段中以 `<程序名>/<x.y.z>` 的形式上报自己的发布版本，服务端 SHALL 在 `hello-ack` 的 `server` 字段中以同样形式返回自己的发布版本。发布版本与协议版本 `version` 相互独立，交换发布版本 MUST NOT 改变协议版本协商的结果。

#### Scenario: 正常交换
- **WHEN** 版本为 `1.0.0` 的客户端连接版本为 `1.0.0` 的服务端
- **THEN** `hello` 中 `client` 为 `link-chat-tui/1.0.0`，`hello-ack` 中 `server` 为 `link-chat/1.0.0`

#### Scenario: 旧客户端不带版本
- **WHEN** 客户端的 `client` 字段缺失或不含合法版本号，且服务端未设置最低客户端版本
- **THEN** 握手照常成功，服务端将该连接的客户端版本记为未知

### Requirement: 客户端版本过旧的拒绝

服务端设置了最低客户端版本时，客户端上报的版本低于该值、缺失或不合法时，服务端 MUST 返回 `code` 为 `client_outdated` 的错误并关闭连接。错误的 `data` MUST 包含 `min_version`（服务端要求的最低版本），并在客户端上报了合法版本时包含 `client_version`。

#### Scenario: 低于最低版本
- **WHEN** 服务端最低客户端版本为 `0.3.0`，版本 `0.2.9` 的客户端发起握手
- **THEN** 服务端返回 `client_outdated`，`data` 中 `min_version` 为 `0.3.0`、`client_version` 为 `0.2.9`，随后关闭连接

#### Scenario: 等于最低版本
- **WHEN** 服务端最低客户端版本为 `0.3.0`，版本 `0.3.0` 的客户端发起握手
- **THEN** 握手成功

#### Scenario: 未上报版本
- **WHEN** 服务端设置了最低客户端版本，客户端的 `client` 字段不含合法版本号
- **THEN** 服务端返回 `client_outdated`，`data` 中只有 `min_version`

### Requirement: 处分相关的错误码

协议 SHALL 新增两个错误码：`account_banned`（账号被封禁）与 `muted`（账号被禁言）。两者的失败响应 MUST 在 `data` 中携带 `reason`（仅 `account_banned`）与 `expires_at`（毫秒时间戳，永久时为 0）。

#### Scenario: 封禁错误携带原因
- **WHEN** 被封禁账号登录被拒
- **THEN** 响应的 `code` 为 `account_banned`，`data` 中包含 `reason` 与 `expires_at`

#### Scenario: 禁言错误携带到期时间
- **WHEN** 被禁言账号发消息被拒
- **THEN** 响应的 `code` 为 `muted`，`data` 中包含 `expires_at`

### Requirement: 处分相关的事件

服务端 SHALL 新增三个事件，均不携带请求 `id`：
- `account.banned`：载荷包含 `reason` 与 `expires_at`，MUST 在关闭连接之前送达。
- `account.muted`：载荷包含 `reason` 与 `expires_at`。
- `account.unmuted`：无必需字段。

#### Scenario: 封禁事件先于断开
- **WHEN** 在线用户被封禁
- **THEN** 其连接先收到 `account.banned`，随后才被关闭

#### Scenario: 旧客户端忽略处分事件
- **WHEN** 未实现这些事件的客户端收到它们
- **THEN** 客户端忽略事件而不报错

### Requirement: 好友相关的请求与事件

协议 SHALL 提供以下请求（均要求已认证，否则返回 `unauthorized`）：
- `friend.request`：`{"username", "note"}`
- `friend.respond`：`{"request_id", "accept"}`
- `friend.list`：返回 `{"friends", "incoming", "blocked"}`
- `friend.remove`、`friend.block`、`friend.unblock`：`{"username"}`

以及事件 `friend.requested`（载荷为申请）、`friend.added`（载荷为新好友）、`friend.removed`（载荷为对方用户 ID，删除好友或拉黑时发给双方，客户端据此移除对方的动态），均不携带请求 `id`。

#### Scenario: 列出好友与申请
- **WHEN** 已登录客户端发送 `friend.list`
- **THEN** 响应包含 `friends`、`incoming`、`blocked` 三个数组，每个用户带用户名、昵称与头像

#### Scenario: 未登录请求
- **WHEN** 未登录的连接发送 `friend.request`
- **THEN** 服务端返回 `unauthorized`

### Requirement: 主页相关的请求与事件

协议 SHALL 提供 `profile.get`（`{"username"}`，返回资料、是否好友、最近动态与游标）、`profile.update`（`{"nickname", "bio", "avatar", "color"}`，省略的字段保持不变），以及事件 `profile.updated`（载荷为更新后的用户资料）。

#### Scenario: 更新部分字段
- **WHEN** 客户端发送只含 `bio` 的 `profile.update`
- **THEN** 只有签名被修改，响应为完整的新资料

### Requirement: 动态相关的请求与事件

协议 SHALL 提供：`moment.post`（`{"body", "visibility"}`，`visibility` 为 `friends` 或 `private`）、`moment.timeline`（`{"cursor"}`）、`moment.delete`、`moment.like`（`{"moment_id", "like"}`）、`moment.comment`（`{"moment_id", "body", "reply_to"}`）、`moment.comment.delete`（`{"comment_id"}`）、`moment.notifications`、`moment.notifications.read`、`moment.unread`（返回 `{"moments", "notifications", "requests"}` 三项计数）。事件：`moment.new`、`moment.updated`（点赞或评论变化后的整条动态）、`moment.deleted`、`moment.notify`（新通知）。

#### Scenario: 时间线分页
- **WHEN** 客户端发送不带游标的 `moment.timeline`，结果多于一页
- **THEN** 响应包含 `moments` 与非空的 `next`，以 `next` 再请求得到下一页

#### Scenario: 旧客户端忽略新事件
- **WHEN** 未实现这些事件的客户端收到 `moment.new`
- **THEN** 客户端忽略事件而不报错，连接保持

### Requirement: 好友、主页与动态的错误码

协议 SHALL 新增错误码：`already_friends`（已是好友）、`invalid_profile`（资料不合规）、`moment_not_found`（动态不存在或不可见）、`moment_too_long`（动态正文过长）、`not_found`（申请或评论不存在）。已有的 `user_not_found`、`forbidden`、`muted`、`rate_limited` 按原含义用于这些请求。

#### Scenario: 动态不可见
- **WHEN** 非好友对某条动态发送 `moment.like`
- **THEN** 响应的 `code` 为 `moment_not_found`

### Requirement: 用户载荷带资料

协议中的用户对象 SHALL 增加 `nickname`、`avatar`、`color` 三个字段，消息对象 SHALL 增加作者的 `author_avatar` 与 `author_color`。旧客户端 MUST 能忽略这些字段。

#### Scenario: 消息带作者头像
- **WHEN** 客户端收到一条消息
- **THEN** 消息中包含作者的头像字符与颜色
