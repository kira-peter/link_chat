# Spec Delta

## MODIFIED Requirements

### Requirement: 区域按权限可见

后台 SHALL 只显示当前账号持有权限对应的区域，并且 MUST 在服务端对每个接口按同样的规则鉴权，而不是只隐藏界面入口。区域与权限的对应关系是：概览需要 `stats.view`；会话、搜索和动态需要 `chat.view`；用户需要 `users.moderate`；账号和审计需要 `accounts.manage`。删除动态或评论另需 `users.moderate`。

#### Scenario: 只能看概览的管理员
- **WHEN** 只持有 `stats.view` 的管理员登录后台
- **THEN** 导航中只有「概览」，没有用户、会话、搜索、动态、审计、账号

#### Scenario: 直接调用无权接口
- **WHEN** 只持有 `stats.view` 的管理员直接请求会话消息接口
- **THEN** 系统返回 `forbidden`

#### Scenario: 只能查看不能删除动态
- **WHEN** 只持有 `chat.view` 的管理员请求删除一条动态
- **THEN** 系统返回 `forbidden`，动态保持不变

## ADDED Requirements

### Requirement: 动态浏览与删除

持有 `chat.view` 的管理员 SHALL 能按时间从新到旧分页查看全部动态（包括仅自己可见的），可按作者用户名筛选；每条显示作者、时间、可见范围、正文、点赞数与全部评论。同时持有 `users.moderate` 的管理员 SHALL 能删除任意动态或评论，删除前 MUST 要求确认。被删除的内容 MUST 从所有用户的界面上消失，作者的在线连接收到相应的删除事件。

#### Scenario: 查看全部动态
- **WHEN** 管理员打开「动态」区域
- **THEN** 看到全部用户的动态，包括仅自己可见的，带可见范围标记

#### Scenario: 删除违规动态
- **WHEN** 持有 `users.moderate` 的管理员删除 `alice` 的一条动态并确认
- **THEN** 这条动态从所有人的时间线消失，审计日志新增一条删除动态的记录
