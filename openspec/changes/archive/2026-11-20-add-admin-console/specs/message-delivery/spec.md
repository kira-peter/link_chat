# Spec Delta

## ADDED Requirements

### Requirement: 禁言期间的发送限制

处于生效禁言中的用户发送消息、编辑消息或创建频道时，系统 MUST 返回 `muted`，载荷包含到期时间（永久时为空）。被拒绝的消息 MUST NOT 被持久化或广播。删除自己的消息、读取历史、加入或离开频道 MUST 不受禁言影响。

#### Scenario: 禁言中发送
- **WHEN** 被禁言的 `dave` 在频道 `general` 发送消息
- **THEN** 系统返回 `muted`，`general` 的成员不收到任何事件

#### Scenario: 禁言中编辑
- **WHEN** 被禁言的 `dave` 编辑自己之前的消息
- **THEN** 系统返回 `muted`，消息内容不变

#### Scenario: 禁言中删除自己的消息
- **WHEN** 被禁言的 `dave` 删除自己之前的消息
- **THEN** 删除成功并照常广播

#### Scenario: 禁言到期后发送
- **WHEN** 禁言到期后 `dave` 发送消息
- **THEN** 消息正常持久化与广播
