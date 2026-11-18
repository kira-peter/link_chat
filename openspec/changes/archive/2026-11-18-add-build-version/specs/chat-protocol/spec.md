# Spec Delta

## ADDED Requirements

### Requirement: 握手交换发布版本

客户端 SHALL 在 `hello` 帧的 `client` 字段中以 `<程序名>/<x.y.z>` 的形式上报自己的发布版本，服务端 SHALL 在 `hello-ack` 的 `server` 字段中以同样形式返回自己的发布版本。发布版本与协议版本 `version` 相互独立，交换发布版本 MUST NOT 改变协议版本协商的结果。

#### Scenario: 正常交换
- **WHEN** 版本为 `1.0.0` 的客户端连接版本为 `1.0.0` 的服务端
- **THEN** `hello` 中 `client` 为 `link-chat-tui/1.0.0`，`hello-ack` 中 `server` 为 `link-chat/1.0.0`

#### Scenario: 旧客户端不带版本
- **WHEN** 客户端的 `client` 字段缺失或不含合法版本号，且服务端未设置最低客户端版本
- **THEN** 握手照常成功，服务端将该连接的客户端版本记为未知

### Requirement: 客户端版本过旧的拒绝

服务端设置了最低客户端版本时，客户端上报的版本低于该值、缺失或不合法时，服务端 MUST 返回 `code` 为 `client_outdated` 的错误并关闭连接。错误的 `data` MUST 包含 `min_version`（服务端要求的最低版本），并在客户端上报了合法版本时包含 `client_version`。

#### Scenario: 低于最低版本
- **WHEN** 服务端最低客户端版本为 `0.3.0`，版本 `0.2.9` 的客户端发起握手
- **THEN** 服务端返回 `client_outdated`，`data` 中 `min_version` 为 `0.3.0`、`client_version` 为 `0.2.9`，随后关闭连接

#### Scenario: 等于最低版本
- **WHEN** 服务端最低客户端版本为 `0.3.0`，版本 `0.3.0` 的客户端发起握手
- **THEN** 握手成功

#### Scenario: 未上报版本
- **WHEN** 服务端设置了最低客户端版本，客户端的 `client` 字段不含合法版本号
- **THEN** 服务端返回 `client_outdated`，`data` 中只有 `min_version`
