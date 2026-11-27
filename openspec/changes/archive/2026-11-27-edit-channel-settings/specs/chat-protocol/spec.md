## MODIFIED Requirements

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
