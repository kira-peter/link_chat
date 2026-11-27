## ADDED Requirements

### Requirement: 编辑频道属性

频道创建者 SHALL 能修改频道名称、简介和可见性。修改 MUST 保留频道 ID、现有成员与历史消息。其他用户编辑频道时，服务端 MUST 返回 `forbidden`。

#### Scenario: 公开频道改为私有
- **WHEN** 创建者将公开频道改为私有
- **THEN** 频道从非成员的会话列表中移除，现有成员仍可访问频道及历史消息

#### Scenario: 私有频道改为公开
- **WHEN** 创建者将私有频道改为公开
- **THEN** 频道向其他已登录的用户公布，并可由任何已认证用户加入
