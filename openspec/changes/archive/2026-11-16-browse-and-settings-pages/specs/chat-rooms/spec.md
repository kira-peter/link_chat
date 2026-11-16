## MODIFIED Requirements

### Requirement: 私聊会话

系统 SHALL 支持两个用户之间的私聊会话。私聊会话 MUST 对第三方不可见，其标识 MUST 对参与双方稳定且一致（无论由谁发起）。私聊会话的名称 MUST 是对方的用户名：同一个私聊，在 `alice` 看来名为 `bob`，在 `bob` 看来名为 `alice`，在会话列表与所有返回会话的响应中都如此。

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

#### Scenario: 私聊的名称
- **WHEN** `alice` 与 `bob` 之间存在私聊，双方各自获取会话列表
- **THEN** `alice` 的列表中该私聊名为 `bob`，`bob` 的列表中该私聊名为 `alice`
