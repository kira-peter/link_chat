# Spec Delta

## ADDED Requirements

### Requirement: 封禁账号的认证拒绝

账号处于生效的封禁中时，登录、用已保存令牌恢复会话、找回密码的任一步骤 MUST 被拒绝，返回 `account_banned`，载荷 MUST 包含封禁原因与到期时间（永久封禁时为空）。为避免借此探测账号是否存在，封禁检查 MUST 在密码校验通过之后进行；找回密码的第一步对被封禁账号 MUST 返回与「无法找回」相同的响应。

#### Scenario: 被封禁账号登录
- **WHEN** 被封禁的 `carol` 以正确密码登录
- **THEN** 系统返回 `account_banned`，载荷包含原因与到期时间，不签发令牌

#### Scenario: 错误密码不暴露封禁
- **WHEN** 有人以错误密码登录被封禁的 `carol`
- **THEN** 系统返回 `invalid_credentials`，与普通账号的错误密码完全一致

#### Scenario: 被封禁账号恢复会话
- **WHEN** 客户端持有封禁前签发的令牌尝试恢复会话
- **THEN** 系统拒绝该请求，令牌已在封禁时被作废

#### Scenario: 被封禁账号找回密码
- **WHEN** 被封禁的 `carol` 发起找回密码
- **THEN** 系统返回 `cannot_recover`，与不存在的账号响应一致

#### Scenario: 封禁到期后可登录
- **WHEN** 封禁到期后 `carol` 以正确密码登录
- **THEN** 登录成功

### Requirement: 管理员创建的账号

由管理员在后台创建的账号 SHALL 与自助注册的账号使用相同的用户名规则与密码存储方式，但没有安全问题，因此 MUST NOT 能自助找回密码；忘记密码时只能由管理员重置。

#### Scenario: 管理员建的号尝试找回
- **WHEN** 管理员创建的账号 `ops` 发起找回密码
- **THEN** 系统返回 `cannot_recover`，客户端提示联系管理员

### Requirement: 账号的管理属性不经聊天协议暴露

账号的管理权限、超级管理员标记与处分记录 MUST NOT 通过聊天协议中的用户列表或任何其他聊天请求返回给客户端。

#### Scenario: 用户列表不含管理信息
- **WHEN** 普通用户请求用户列表
- **THEN** 返回的每个用户只包含已有的公开字段，不包含权限或处分信息
