# Spec Delta

## ADDED Requirements

### Requirement: 后台会话不受单会话约束

单会话约束 SHALL 只适用于聊天会话。后台会话 MUST NOT 计入「已有会话」，建立或结束后台会话 MUST NOT 吊销或通知任何聊天会话；建立聊天会话也 MUST NOT 吊销后台会话。

#### Scenario: 聊天登录不踢掉后台
- **WHEN** 管理员 `bob` 已登录后台，又在聊天客户端登录
- **THEN** 后台会话保持有效

#### Scenario: 后台登录不顶号
- **WHEN** `bob` 已在聊天客户端在线，又登录后台
- **THEN** 聊天连接不收到 `session.replaced`

### Requirement: 强制下线按顶号处理

被管理员强制下线的连接 SHALL 收到 `session.replaced` 后被关闭，事件中的来源标记为由管理员执行，客户端按会话被顶掉的规则处理。

#### Scenario: 被强制下线的客户端
- **WHEN** 管理员让在线的 `erin` 强制下线
- **THEN** `erin` 的客户端显示会话已被结束的提示，停止自动重连
