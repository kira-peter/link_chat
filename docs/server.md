# link_chat 服务端

服务端是一个单进程的 WebSocket 聊天服务，用 Go 编写，数据以 JSON 文件存放。

## 构建与启动

```bash
make build-server          # 产物：build/link-chat-server
./build/link-chat-server -config server.json -data ./data
```

首次启动会在 `-config` 指定的路径生成一份带注释的默认配置，并创建数据目录。

验证服务是否可用：

```bash
curl -s http://127.0.0.1:8100/healthz     # 正常返回 ok
```

## 配置项

配置文件是 JSON。所有字段都有默认值，只有 `listen` 和 `data_dir` 是必需的——缺少它们服务端会以非零状态码退出并指出具体字段。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `listen` | `:8100` | 监听地址，格式 `host:port` |
| `data_dir` | `./data` | 数据目录，必须可写 |
| `public_url` | 空 | 展示给客户端的地址提示，可选 |
| `tls_cert` / `tls_key` | 空 | 同时设置即启用直连 TLS |
| `trust_proxy` | `false` | 是否采信 `X-Forwarded-For` |
| `max_frame_bytes` | `65536` | 单帧上限 |
| `max_message_bytes` | `4000` | 单条消息正文上限 |
| `requests_per_second` | `20` | 单连接请求速率上限 |
| `heartbeat_seconds` | `30` | 心跳周期。服务端在 `2×` 该时长内没收到任何**应用帧**就判定断线 |
| `history_page_size` | `50` | 历史分页默认条数 |
| `send_queue_depth` | `256` | 单连接发送队列深度，满则断开该连接 |
| `login_attempts_per_minute` | `5` | 单来源登录尝试上限 |
| `register_per_minute` | `5` | 单来源注册上限 |
| `shutdown_grace_seconds` | `10` | 关停时等待在途请求的上限 |
| `log_level` | `info` | `debug` / `info` / `warn` / `error` |

### 环境变量覆盖

以下变量优先于配置文件，便于容器部署：

```
LINKCHAT_LISTEN      LINKCHAT_DATA_DIR     LINKCHAT_LOG_LEVEL
LINKCHAT_TLS_CERT    LINKCHAT_TLS_KEY      LINKCHAT_TRUST_PROXY
```

## TLS 的两种方式

**方式一：服务端直连 TLS。** 同时提供证书与私钥，服务端只接受加密连接。

```json
{ "tls_cert": "/etc/link-chat/fullchain.pem", "tls_key": "/etc/link-chat/privkey.pem" }
```

证书文件不存在或与私钥不匹配时服务端拒绝启动，并在错误里指明是哪一个文件。

**方式二：反向代理终止 TLS。** 服务端明文监听，由 nginx/Caddy 转发。此时必须设置 `trust_proxy: true`，否则服务端看不到真实来源地址，限流会把所有流量算作同一个来源：

```nginx
location /ws {
    proxy_pass http://127.0.0.1:8100;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_read_timeout 120s;
}
```

**`trust_proxy` 关闭时，客户端自报的来源头一律被忽略**，服务端以实际连接地址为准。在反向代理之后却忘了打开它，限流会全站共享一个额度——这是最容易踩的一个坑。

## 数据目录布局

```
data/
├── users.json                账号（密码为 Argon2id 哈希）
├── tokens.json               会话令牌（只存 SHA-256 哈希）
├── conversations.json        频道与私聊元数据
├── members.json              频道成员关系
└── messages/
    ├── c_<id>.jsonl          每个会话一个追加日志
    └── dm_<id>.jsonl
```

消息按会话分文件并采用追加写（JSONL），所以发一条消息只追加一行，不会重写整个会话历史。改消息与删消息会重写该会话的文件——这两种操作不频繁，代价可以接受。

元数据文件较小，每次变更整体原子重写：写临时文件 → `fsync` → `rename` 覆盖。进程在任意时刻崩溃，磁盘上要么是旧的完整版本，要么是新的完整版本。

## 容量与性能

在本机（Intel Core Ultra 7 265K，Windows 11）实测的持久化写入延迟：

| 会话已有消息数 | 单条发送耗时 |
|---|---|
| 0 | 约 1.6 ms |
| 1,000 | 约 2.2 ms |
| 10,000 | 约 1.7 ms |
| 100,000 | 约 1.6 ms |

关键结论：**延迟与会话历史长度无关**。这正是按会话分片 + 追加写想要的效果。单条发送的成本基本就是一次 `fsync` 的磁盘往返，在普通 SSD 上约 1–2 ms。

据此估算：单实例的写入吞吐上限在几百条/秒量级（串行化写入是有意为之的设计，见 `design.md` 决策 4）。对几十到几百人的团队规模足够。

**何时该换存储：** 需要多机部署、或者需要按内容搜索、或者写入量持续超过上述量级时。`Storage` 接口是唯一的替换点，换成 SQLite 只需要新增一个实现，业务层不动。

**启动成本：** 服务端启动时把全部数据载入内存，读操作零磁盘 IO。十万条消息的量级启动在百毫秒级。数据目录增长到百万级之前，这个模型没有问题。

## 运维

**优雅关停。** 收到 `SIGINT` / `SIGTERM` 后：停止接受新连接 → 向所有客户端推送 `server.shutdown` 事件 → 等待在途请求完成（上限 `shutdown_grace_seconds`）→ 退出。已向发送方确认的消息在关停后必然存在于磁盘上。

**心跳与断线判定。** 客户端每 `heartbeat_seconds / 2` 主动发一个 `presence.ping`，服务端回 `pong`；任何合法帧都会重置读超时。WebSocket 的 Ping 控制帧在库内部处理，**不会**重置这个超时，所以心跳必须是应用层请求——这也是客户端必须升级到带心跳的版本的原因：旧客户端空闲 60 秒就会被断开。

**单会话登录。** 同一账号只保留一个有效会话。建立新会话的**任何**途径都会顶号，包括客户端用已保存令牌自动恢复（`auth.resume`）——不只是显式登录。服务端会吊销该账号的全部旧会话令牌并关闭旧连接，旧连接在关闭前收到 `session.replaced` 事件，其中带顶号方的来源地址与设备名。

恢复会话时令牌会**轮换**：被出示的旧令牌立即作废，新令牌返回给客户端。这是必要的，否则被顶掉的一端拿着仍然有效的旧令牌重连，就会把对方顶回去，两端反复互踢。客户端因此必须保存响应里的新令牌。

`reset` 令牌不受影响，找回密码流程不会被登录打断。

**日志。** 记录连接建立与断开、认证失败、限流触发、会话被顶号、持久化错误与关停过程。日志中不含密码、令牌原文或消息正文；顶号时会记录新登录的来源地址与设备名。

**备份。** 停止服务后直接复制整个数据目录即可，文件之间没有跨文件事务。运行中备份请用文件系统快照，避免复制到写了一半的日志。

**密码与令牌。** 密码用 Argon2id（m=64MiB, t=3, p=2）加盐哈希，参数变更后用户下次登录会自动重哈希。令牌是 32 字节随机值，磁盘上只存哈希，因此泄露数据文件不等于泄露会话。

## 故障排查

| 现象 | 原因 |
|---|---|
| 启动即退出，提示 `listen is required` | 配置缺少必填项 |
| 启动即退出，提示 `data_dir is not writable` | 数据目录权限不足 |
| 客户端全部报 `rate_limited` | 反向代理后没开 `trust_proxy`，所有请求算作同一来源 |
| 客户端频繁断线重连 | 反向代理的读超时短于心跳周期，调大 `proxy_read_timeout` |
| `/healthz` 返回 503 | 进程正在关停，或数据目录变为不可写 |
| 重启后消息缺失 | 检查 `messages/*.jsonl` 最后一行是否被截断（进程被杀时的预期行为，已确认的消息不受影响） |
