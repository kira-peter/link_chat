# Spec Delta

## MODIFIED Requirements

### Requirement: 心跳与超时

连接 MUST 通过双向心跳检测存活，且客户端 MUST 主动发送心跳使空闲连接保持有效。任何一端在连续两个心跳周期内未收到对端任何帧，MUST 视为断线并关闭连接。

#### Scenario: 服务端检测到失联客户端
- **WHEN** 客户端在 `heartbeat_interval * 2` 内未发送任何帧
- **THEN** 服务端关闭该连接，并像正常断开一样更新其在线状态

#### Scenario: 客户端检测到服务端失联
- **WHEN** 客户端在 `heartbeat_interval * 2` 内未收到服务端任何帧
- **THEN** 客户端关闭本地连接并进入重连流程

#### Scenario: 心跳不打断空闲会话
- **WHEN** 用户 10 分钟未发送任何消息但客户端持续心跳
- **THEN** 连接保持建立，服务端不将其判定为离线

#### Scenario: 客户端主动心跳保持空闲连接
- **WHEN** 用户长时间不操作键盘、也未收发任何消息
- **THEN** 客户端按心跳周期主动发送心跳请求，服务端回以心跳响应，连接不被读超时关闭

#### Scenario: 心跳帧重置读超时
- **WHEN** 服务端收到任意合法帧（含心跳请求）
- **THEN** 该连接的读超时从此刻重新计算

### Requirement: 请求与响应关联

客户端发起的请求 MUST 携带唯一 `id`，服务端返回的响应 MUST 回带同一个 `id`。服务端主动推送的事件 MUST NOT 携带请求 `id`。心跳请求与其他请求遵循同一组关联规则。

#### Scenario: 请求得到对应响应
- **WHEN** 客户端发送 `id` 为 `a1` 的 `send-message` 请求
- **THEN** 服务端返回 `id` 为 `a1` 的响应，且该响应只有一条

#### Scenario: 事件不带请求 id
- **WHEN** 其他用户向同一频道发送消息
- **THEN** 客户端收到 `type` 为 `message` 的事件帧，其中不存在 `id` 字段

#### Scenario: 重复的请求 id
- **WHEN** 客户端在尚未收到响应前用相同 `id` 发出第二个请求
- **THEN** 服务端返回错误码 `duplicate_request_id` 且不重复执行该请求的副作用

#### Scenario: 心跳请求得到关联响应
- **WHEN** 客户端发送带 `id` 的心跳请求
- **THEN** 服务端返回回带同一 `id` 的响应，客户端据此确认链路存活

## ADDED Requirements

### Requirement: 会话被顶掉事件

服务端 SHALL 在被顶掉的连接关闭前推送 `session.replaced` 事件，载荷 MUST 包含账号名、顶号方来源地址与时间戳，并可包含设备名。该事件 MUST NOT 携带请求 `id`。

#### Scenario: 事件字段完整
- **WHEN** 某会话被顶掉
- **THEN** 事件载荷包含 `username`、`location`、`at` 三个字段，`device` 在客户端上报时出现

#### Scenario: 事件在断开之前送达
- **WHEN** 服务端决定断开被顶掉的连接
- **THEN** 事件先写入该连接，随后才关闭，客户端不会错过通知

#### Scenario: 旧客户端忽略未知事件
- **WHEN** 未实现该事件的客户端收到它
- **THEN** 客户端忽略该事件而不报错，连接仍会随后关闭
