# chat-protocol Specification

## Purpose
定义客户端与服务端之间的 WebSocket 通信契约：帧结构、版本协商、请求/响应/事件三类消息、错误码、心跳与超时。任何客户端实现只要遵守本契约即可与服务端互通，不依赖具体语言或库。

## Requirements

### Requirement: 协议版本协商

每一端 MUST 在连接建立后的第一条消息中声明其协议版本。服务端 SHALL 在能力不匹配时明确拒绝，而不是降级为不可预测的行为。

#### Scenario: 版本一致时正常握手
- **WHEN** 客户端发送 `hello` 帧并携带 `version: 1`
- **THEN** 服务端返回 `hello-ack` 帧，其中 `version` 为服务端支持的版本，连接进入已建立状态

#### Scenario: 客户端版本过高
- **WHEN** 客户端发送的 `version` 大于服务端支持的最高版本
- **THEN** 服务端返回错误码 `protocol_version_unsupported` 并关闭连接，错误体包含服务端支持的版本范围

#### Scenario: 首个帧不是 hello
- **WHEN** 连接建立后客户端发送的第一个帧类型不是 `hello`
- **THEN** 服务端返回错误码 `protocol_violation` 并立即关闭连接

### Requirement: 帧信封结构

所有帧 MUST 是一个 JSON 对象，包含 `type`、`id`（可选）与 `payload` 三个字段。未知的额外字段 MUST 被忽略而非导致解析失败，以便未来扩展不破坏旧客户端。

#### Scenario: 合法帧被解析
- **WHEN** 服务端收到 `{"type":"ping","id":"7","payload":{}}`
- **THEN** 服务端成功解析该帧并处理 `ping`

#### Scenario: 帧中带有未知字段
- **WHEN** 客户端在 `payload` 中加入服务端不认识的字段 `futureField`
- **THEN** 服务端忽略该字段并正常处理帧，不返回解析错误

#### Scenario: 帧不是合法 JSON
- **WHEN** 服务端收到无法解析的字节序列
- **THEN** 服务端返回错误码 `malformed_frame`，连接保持打开，连续违规达到阈值时关闭连接

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

### Requirement: 错误表示

所有失败响应 MUST 包含机器可读的 `code` 与人类可读的 `message`。错误码集合 SHALL 稳定，新增错误码不得复用已有字符串。

#### Scenario: 参数校验失败
- **WHEN** 客户端发送的消息体为空字符串
- **THEN** 服务端返回 `code` 为 `invalid_argument` 的响应，且消息未被广播

#### Scenario: 未认证访问
- **WHEN** 尚未登录的连接发送 `send-message` 请求
- **THEN** 服务端返回 `code` 为 `unauthorized` 的响应

#### Scenario: 权限不足
- **WHEN** 非频道成员发送向该频道发消息的请求
- **THEN** 服务端返回 `code` 为 `forbidden` 的响应

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

### Requirement: 帧大小与速率上限

服务端 MUST 限制单帧大小与单位时间内的帧数量，超限时拒绝并说明原因，避免单个连接耗尽服务端资源。

#### Scenario: 超大帧被拒绝
- **WHEN** 客户端发送的帧超过配置的 `max_frame_bytes`
- **THEN** 服务端返回 `code` 为 `frame_too_large` 的响应且不处理该帧内容

#### Scenario: 突发请求被限流
- **WHEN** 同一连接在 1 秒内发出的请求数超过配置的 `requests_per_second`
- **THEN** 服务端对超出部分返回 `code` 为 `rate_limited` 的响应，并附带建议的重试等待毫秒数

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
