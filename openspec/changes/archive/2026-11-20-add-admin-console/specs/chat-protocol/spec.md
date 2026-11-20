# Spec Delta

## ADDED Requirements

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
