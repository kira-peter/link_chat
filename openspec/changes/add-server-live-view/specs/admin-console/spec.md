# Spec Delta

## MODIFIED Requirements

### Requirement: 区域按权限可见

后台 SHALL 只显示当前账号持有权限对应的区域，并且 MUST 在服务端对每个接口按同样的规则鉴权，而不是只隐藏界面入口。区域与权限的对应关系是：概览与实况需要 `stats.view`；会话、搜索和动态需要 `chat.view`；用户需要 `users.moderate`；账号和审计需要 `accounts.manage`。删除动态或评论另需 `users.moderate`。实况页中的消息内容另需 `chat.view`。

#### Scenario: 只能看概览的管理员
- **WHEN** 只持有 `stats.view` 的管理员登录后台
- **THEN** 导航中只有「概览」与「实况」，没有用户、会话、搜索、动态、审计、账号

#### Scenario: 直接调用无权接口
- **WHEN** 只持有 `stats.view` 的管理员直接请求会话消息接口
- **THEN** 系统返回 `forbidden`

#### Scenario: 只能查看不能删除动态
- **WHEN** 只持有 `chat.view` 的管理员请求删除一条动态
- **THEN** 系统返回 `forbidden`，动态保持不变

#### Scenario: 没有统计权限看不到实况
- **WHEN** 只持有 `users.moderate` 的管理员登录后台
- **THEN** 导航中没有「实况」，直接请求实况数据返回 `forbidden`
