## ADDED Requirements

### Requirement: 账号设置的请求与事件

协议 SHALL 提供读取与保存当前账号设置的两个请求，以及一个设置已更新事件：

- `settings.get`：无参数。响应载荷为 `{"settings": <设置对象>, "exists": <布尔>}`；该账号从未保存过设置时 `exists` 为 `false`，`settings` 为空对象。
- `settings.put`：载荷为 `{"settings": <设置对象>}`，只包含要修改的项。成功时响应载荷为保存后的完整 `{"settings": <设置对象>}`。
- `settings.updated` 事件：载荷为保存后的完整 `{"settings": <设置对象>}`，不携带请求 `id`。

未认证的连接发出这两个请求时 MUST 返回 `unauthorized`。

#### Scenario: 读取尚未保存的设置
- **WHEN** 新注册的账号发送 `settings.get`
- **THEN** 响应中 `exists` 为 `false`，`settings` 为空对象

#### Scenario: 保存后读取
- **WHEN** 客户端发送 `settings.put`，载荷为 `{"settings": {"theme": "paper"}}`
- **THEN** 响应载荷中 `settings.theme` 为 `"paper"`，之后的 `settings.get` 返回 `exists` 为 `true` 且主题相同

#### Scenario: 未登录请求设置
- **WHEN** 尚未登录的连接发送 `settings.get`
- **THEN** 服务端返回 `unauthorized`

#### Scenario: 旧客户端忽略设置事件
- **WHEN** 未实现 `settings.updated` 的客户端收到该事件
- **THEN** 客户端忽略该事件而不报错，连接保持

### Requirement: 设置对象的键与校验

设置对象的键 MUST 为以下之一：`theme`、`motion`、`language`、`bubble_width`、`density`、`time_format`、`msg_border`、`msg_time`、`self_text`、`other_text`、`self_border`、`other_border`，值均为字符串。`settings.put` 中出现未知的键或不在候选内的值时，服务端 MUST 返回 `invalid_argument`，且不保存任何项。

#### Scenario: 未知的键
- **WHEN** `settings.put` 载荷包含键 `font_size`
- **THEN** 服务端返回 `invalid_argument`，不保存任何项

#### Scenario: 不在候选内的值
- **WHEN** `settings.put` 载荷中 `bubble_width` 为 `"55"`
- **THEN** 服务端返回 `invalid_argument`，不保存任何项

### Requirement: 服务端不支持设置请求时的识别

服务端不认识 `settings.get`、`settings.put` 时，返回 `invalid_argument`（旧服务端对未知请求的既有行为）。因为 `settings.get` 没有参数，不可能有其他参数错误，客户端 MUST 把 `settings.get` 得到的 `invalid_argument` 视为服务端不支持设置同步。

#### Scenario: 旧服务端的响应
- **WHEN** 客户端向没有设置请求的旧服务端发送 `settings.get`
- **THEN** 响应的错误码为 `invalid_argument`，客户端据此判定服务端不支持设置同步
