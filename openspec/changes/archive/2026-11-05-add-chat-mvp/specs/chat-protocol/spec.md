# Spec Delta

## Purpose

定义客户端与服务端之间的 WebSocket 通信契约：帧结构、版本协商、请求/响应/事件三类消息、错误码、心跳与超时。任何客户端实现只要遵守本契约即可与服务端互通，不依赖具体语言或库。

## ADDED Requirements

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

客户端发起的请求 MUST 携带唯一 `id`，服务端返回的响应 MUST 回带同一个 `id`。服务端主动推送的事件 MUST NOT 携带请求 `id`。

#### Scenario: 请求得到对应响应
- **WHEN** 客户端发送 `id` 为 `a1` 的 `send-message` 请求
- **THEN** 服务端返回 `id` 为 `a1` 的响应，且该响应只有一条

#### Scenario: 事件不带请求 id
- **WHEN** 其他用户向同一频道发送消息
- **THEN** 客户端收到 `type` 为 `message` 的事件帧，其中不存在 `id` 字段

#### Scenario: 重复的请求 id
- **WHEN** 客户端在尚未收到响应前用相同 `id` 发出第二个请求
- **THEN** 服务端返回错误码 `duplicate_request_id` 且不重复执行该请求的副作用

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

连接 MUST 通过双向心跳检测存活。任何一端在连续两个心跳周期内未收到对端任何帧，MUST 视为断线并关闭连接。

#### Scenario: 服务端检测到失联客户端
- **WHEN** 客户端在 `heartbeat_interval * 2` 内未发送任何帧
- **THEN** 服务端关闭该连接，并像正常断开一样更新其在线状态

#### Scenario: 客户端检测到服务端失联
- **WHEN** 客户端在 `heartbeat_interval * 2` 内未收到服务端任何帧
- **THEN** 客户端关闭本地连接并进入重连流程

#### Scenario: 心跳不打断空闲会话
- **WHEN** 用户 10 分钟未发送任何消息但客户端持续心跳
- **THEN** 连接保持建立，服务端不将其判定为离线

### Requirement: 帧大小与速率上限

服务端 MUST 限制单帧大小与单位时间内的帧数量，超限时拒绝并说明原因，避免单个连接耗尽服务端资源。

#### Scenario: 超大帧被拒绝
- **WHEN** 客户端发送的帧超过配置的 `max_frame_bytes`
- **THEN** 服务端返回 `code` 为 `frame_too_large` 的响应且不处理该帧内容

#### Scenario: 突发请求被限流
- **WHEN** 同一连接在 1 秒内发出的请求数超过配置的 `requests_per_second`
- **THEN** 服务端对超出部分返回 `code` 为 `rate_limited` 的响应，并附带建议的重试等待毫秒数
