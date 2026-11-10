# Proposal

## Why

三个问题，都是实际使用中立刻会撞到的：

**1. 打开就自动登录，两个客户端用的是同一个身份。** 客户端把令牌存在全局配置 `%APPDATA%\link-chat\config.json` 里。在同一台机器上开第二个客户端，它读到同一份令牌直接 resume，于是两个窗口都是同一个人——没法用两个账号测试，也没法让第二个人用同一台机器。用户需要的是「同一台机器上可以登录不同账号」。

**2. 空闲一分钟后必定断线重连。** 服务端 `readLoop` 对每次 `Read` 设了 `heartbeat_interval * 2`（默认 60 秒）的超时，但客户端从不主动发任何帧。WebSocket 的 Ping 是控制帧，不会重置这个读超时，所以只要用户不敲键盘，60 秒后服务端就把连接判死。规格里写着「客户端持续心跳」，实现里没有。

**3. 没有「在别处登录」的提示。** 当前实现允许多端同时在线，且第二处登录不会通知第一处。用户希望的是相反的行为：后登录的顶掉先登录的，并且先登录的那一方要收到明确提示，附带登录地点。

## What Changes

**客户端身份按实例隔离**

- 默认配置文件改为按「实例」存放：`%APPDATA%\link-chat\instances\<name>.json`，`<name>` 由 `-as` 参数指定，未指定时为 `default`。
- 在同一台机器上开多个客户端，各自用 `-as alice` / `-as bob` 就互不干扰。
- `-config` 显式指定路径时行为不变。

**客户端主动心跳**

- 客户端原先没有任何心跳，现在每 `heartbeat_interval / 2` 发送一个 `presence.ping` 请求，服务端回 `pong`。
- 服务端读超时从「任何帧」严格重置，空闲连接不再被误杀。
- 断线检测保留：真正失联时两端仍能各自发现。

**单会话登录（新行为，**BREAKING**）**

- 同一账号在第二处登录时，服务端吊销第一处的会话并主动断开它。
- 第一处的客户端收到 `session.replaced` 事件，弹出提示：账号已在别处登录，附带登录地点（来源 IP 与大致时间），并提供「重新登录」入口。
- 被顶掉的连接不可自动重连：它的令牌已失效，客户端 MUST 停在提示界面而不是进入重连循环。

## Capabilities

### New Capabilities

- `login-session`：单会话登录的语义——顶号、通知、地点信息、被顶掉一端的后续行为。

### Modified Capabilities

- `account-auth`：令牌签发时的会话唯一性（吊销同用户的其他会话）、`session.replaced` 的触发条件与载荷。
- `chat-protocol`：客户端心跳的具体形式（主动请求 + `pong`）、读超时的重置规则、新增 `session.replaced` 事件。
- `tui-chat-client`：客户端身份按实例隔离的配置路径、被顶掉时的提示界面与后续行为。

## Impact

**协议**（`pkg/protocol`）

- 新增事件 `session.replaced`，载荷含 `username`、`location`（IP 与地理提示）、`at`（毫秒时间戳）、`device`（可选，客户端上报的名称）。
- `AuthRes` 增加 `device` 回显，便于在提示里显示「哪个客户端」。

**存储**（`pkg/storage`）

- `Token` 增加 `Remote`、`Device`、`LastSeenAt` 字段，用于在顶号时说明来源。
- 新增 `ListTokensForUser(userID)`，供吊销时构造提示。

**服务端**（`pkg/server`）

- 登录成功时吊销该用户其他 `session` 令牌（不影响 `reset` 令牌）。
- 被顶掉的连接由 Hub 定位并推送 `session.replaced` 后关闭。
- 读超时语义明确化：任何帧（含 ping 请求）都重置。
- 记录每次连接的来源地址，写入令牌。

**客户端**（`pkg/app`、`pkg/client`）

- 配置文件按 `-as` 落到 `instances/<name>.json`。
- `Transport` 增加心跳协程。
- 收到 `session.replaced` 时停止重连、清理本地令牌、打开提示对话框。

**i18n**：新增顶号提示相关文案（中英）。

**文档**：`docs/client.md` 补 `-as` 用法与多账号说明。
