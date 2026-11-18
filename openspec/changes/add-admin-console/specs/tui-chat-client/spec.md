# Spec Delta

## ADDED Requirements

### Requirement: 被封禁时的界面

客户端登录或恢复会话收到 `account_banned`，或在线时收到 `account.banned` 时，MUST 停止自动重连、清除该账号的本地令牌，并在登录界面显示账号已被封禁、原因与解封时间（永久封禁显示「永久」）。

#### Scenario: 在线时被封禁
- **WHEN** 客户端在线时收到 `account.banned`
- **THEN** 客户端回到登录界面，显示封禁原因与解封时间，不进入重连

#### Scenario: 登录时被拒
- **WHEN** 用户登录时服务端返回 `account_banned`
- **THEN** 登录面板的错误信息行显示封禁原因与解封时间，保留用户名

### Requirement: 被禁言时的界面

客户端收到 `account.muted`，或发送时收到 `muted` 时，MUST 使输入区不可输入，并在输入区显示「禁言至 <时间>」（永久时显示「已被禁言」）。收到 `account.unmuted` 或到达到期时间后 MUST 自动恢复输入。被禁言期间发送失败的内容 MUST 留在草稿中。

#### Scenario: 收到禁言事件
- **WHEN** 客户端收到 `account.muted`
- **THEN** 输入区变为不可输入，显示禁言到期时间

#### Scenario: 禁言到期自动恢复
- **WHEN** 禁言的到期时间到达
- **THEN** 输入区恢复可输入，无需重新登录

#### Scenario: 发送被拒保留草稿
- **WHEN** 用户发送时收到 `muted`
- **THEN** 输入内容保留在输入区，输入区进入禁言状态
