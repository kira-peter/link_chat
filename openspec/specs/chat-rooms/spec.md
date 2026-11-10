# chat-rooms Specification

## Purpose
定义频道与私聊会话的模型：创建、加入、退出、成员查询，以及会话元数据的可见性规则。会话是消息投递的边界，本能力决定"谁能看到什么"。

## Requirements

### Requirement: 频道创建

系统 SHALL 允许已认证用户创建公开频道，要求频道名在系统中唯一。创建者自动成为频道成员。

#### Scenario: 创建公开频道
- **WHEN** 用户 `alice` 创建名为 `general` 的频道
- **THEN** 系统创建频道，`alice` 成为其成员，频道出现在所有用户的频道列表中

#### Scenario: 频道名重复
- **WHEN** 用户创建已存在的频道名 `General`（已有频道为 `general`）
- **THEN** 系统返回 `code` 为 `channel_exists` 的响应且不创建频道

#### Scenario: 频道名非法
- **WHEN** 用户提交包含空格、斜杠或控制字符的频道名
- **THEN** 系统返回 `code` 为 `invalid_channel_name` 的响应

#### Scenario: 未认证用户创建频道
- **WHEN** 未认证连接请求创建频道
- **THEN** 系统返回 `code` 为 `unauthorized` 的响应

### Requirement: 频道加入与退出

已认证用户 SHALL 能加入公开频道，也能退出已加入的频道。加入后 MUST 立即获得该频道的历史消息，退出后 MUST NOT 再收到该频道的新消息。

#### Scenario: 加入频道
- **WHEN** 用户加入已存在的公开频道
- **THEN** 系统将其加入成员列表，返回成功响应，并发送该频道最近的历史消息

#### Scenario: 重复加入幂等
- **WHEN** 已是成员的用户再次请求加入同一频道
- **THEN** 系统返回成功响应，成员列表不出现重复项，也不重复发送历史

#### Scenario: 退出频道
- **WHEN** 用户退出所属频道
- **THEN** 系统将其移出成员列表，该用户不再收到此频道的消息事件

#### Scenario: 加入不存在的频道
- **WHEN** 用户请求加入不存在的频道
- **THEN** 系统返回 `code` 为 `channel_not_found` 的响应

### Requirement: 私聊会话

系统 SHALL 支持两个用户之间的私聊会话。私聊会话 MUST 对第三方不可见，其标识 MUST 对参与双方稳定且一致（无论由谁发起）。

#### Scenario: 发起私聊
- **WHEN** 用户 `alice` 向 `bob` 发起私聊并发送第一条消息
- **THEN** 系统创建一个双方共享的私聊会话，`alice` 与 `bob` 都收到该消息

#### Scenario: 双方得到相同会话标识
- **WHEN** `alice` 先向 `bob` 发私聊，随后 `bob` 在客户端打开与 `alice` 的会话
- **THEN** 双方使用同一个会话标识，历史消息一致

#### Scenario: 第三方无法访问
- **WHEN** 用户 `carol` 尝试向不属于她的私聊会话发送消息
- **THEN** 系统返回 `code` 为 `forbidden` 的响应，且 `carol` 无法读取该会话历史

#### Scenario: 向自己发起私聊
- **WHEN** 用户向自己发起私聊
- **THEN** 系统返回 `code` 为 `invalid_argument` 的响应

### Requirement: 会话元数据

每个会话 SHALL 暴露名称、类型（频道或私聊）、成员数、最近消息时间等元数据。频道元数据对全体已认证用户可见；私聊元数据仅对参与者可见。

#### Scenario: 列出可见会话
- **WHEN** 已认证用户请求会话列表
- **THEN** 系统返回全部公开频道与该用户参与的私聊会话，不含他人的私聊

#### Scenario: 私聊不暴露成员
- **WHEN** 用户请求他人私聊会话的元数据
- **THEN** 系统返回 `code` 为 `forbidden` 的响应，且响应中不包含成员信息

#### Scenario: 成员数随加入退出变化
- **WHEN** 用户加入或退出某频道
- **THEN** 该频道的成员数在后续元数据查询中反映最新值

### Requirement: 会话删除

频道创建者 SHALL 能删除自己创建的频道。删除后该频道 MUST 从所有用户的会话列表中消失，其消息不再可访问。

#### Scenario: 创建者删除频道
- **WHEN** 频道创建者请求删除该频道
- **THEN** 系统删除频道、移除全部成员关系，其他用户不再看到该频道

#### Scenario: 非创建者删除频道
- **WHEN** 非创建者请求删除某频道
- **THEN** 系统返回 `code` 为 `forbidden` 的响应

#### Scenario: 删除后发消息失败
- **WHEN** 用户在频道被删除后向该频道发送消息
- **THEN** 系统返回 `code` 为 `channel_not_found` 的响应

### Requirement: 在线状态

系统 SHALL 跟踪用户在线状态，并在用户上线、下线时向相关会话广播状态变化。用户有多条活跃连接时，MUST 在该用户的最后一条连接关闭后才判定为离线。

#### Scenario: 用户上线广播
- **WHEN** 用户凭据认证成功并建立连接
- **THEN** 与该用户共享会话的其他用户收到该用户的在线状态事件

#### Scenario: 多端登录不误判离线
- **WHEN** 用户关闭其中一个客户端但仍保留另一个连接
- **THEN** 系统不广播该用户离线

#### Scenario: 全部连接关闭后离线
- **WHEN** 用户的最后一条连接断开
- **THEN** 系统广播该用户离线，并保留其在各会话中的成员身份
