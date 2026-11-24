# Spec Delta

## ADDED Requirements

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
