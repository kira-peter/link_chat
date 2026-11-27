# link_chat 服务端

服务端是一个单进程的 WebSocket 聊天服务，用 Go 编写，数据存放在本地 SQLite 数据库中。

## 构建与启动

```bash
make build-server          # 产物：build/link-chat-server
./build/link-chat-server -config server.json -data ./data
```

首次启动会在 `-config` 指定的路径生成一份带注释的默认配置，并创建数据目录。

查看版本：

```bash
./build/link-chat-server -version     # 输出 link-chat-server 6.0.0
```

服务端和客户端的版本号相同，都来自仓库根目录的 `VERSION` 文件，编译时打进二进制。

验证服务是否可用：

```bash
curl -s http://127.0.0.1:8100/healthz     # 正常返回 ok
```

## 配置项

配置文件是 JSON。所有字段都有默认值，只有 `listen` 和 `data_dir` 是必需的——缺少它们服务端会以非零状态码退出并指出具体字段。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `listen` | `127.0.0.1:8100` | 监听地址，格式 `host:port` |
| `data_dir` | `./data` | 数据目录，必须可写 |
| `public_url` | 空 | 展示给客户端的地址提示，可选 |
| `tls_cert` / `tls_key` | 空 | 同时设置即启用直连 TLS |
| `trust_proxy` | `false` | 是否采信受信代理发送的转发头 |
| `trusted_proxy_cidrs` | `["127.0.0.0/8", "::1/128"]` | 受信代理的 IP 网段 |
| `max_frame_bytes` | `65536` | 单帧上限 |
| `max_message_bytes` | `4000` | 单条消息正文上限 |
| `requests_per_second` | `20` | 单连接请求速率上限 |
| `heartbeat_seconds` | `30` | 心跳周期。服务端在 `2×` 该时长内没收到任何**应用帧**就判定断线 |
| `history_page_size` | `50` | 历史分页默认条数 |
| `send_queue_depth` | `256` | 单连接发送队列深度，满则断开该连接 |
| `login_attempts_per_minute` | `5` | 单来源登录尝试上限 |
| `register_per_minute` | `5` | 单来源注册上限 |
| `friend_requests_per_hour` | `30` | 每个账号每小时最多发出的好友申请 |
| `moments_per_hour` | `20` | 每个账号每小时最多发布的动态 |
| `interactions_per_minute` | `30` | 每个账号每分钟最多的点赞加评论次数 |
| `shutdown_grace_seconds` | `10` | 关停时等待在途请求的上限 |
| `log_level` | `info` | `debug` / `info` / `warn` / `error` |
| `admin_enabled` | `true` | 是否在同一地址的 `/admin/` 提供管理后台。关闭后 `/admin/` 下所有路径返回 404 |
| `min_client_version` | 空 | 最低客户端版本，格式 `x.y.z`。设置后，版本更低或不上报版本的客户端在握手时被拒绝（错误码 `client_outdated`）；空表示不限制。格式不合法时服务端拒绝启动 |

### 登录失败的等待时间

聊天客户端连续输错密码后，服务端按下面的阶梯拒绝后续尝试，并通过 `rate_limited` 的 `retry_after_ms` 告诉客户端还要等多久：

| 连续输错次数 | 之后的等待 |
|---|---|
| 1–3 | 无等待，可以马上重试 |
| 4 | 1 分钟 |
| 5 | 5 分钟 |
| 6 | 15 分钟 |
| 7 | 1 小时 |
| 8 | 3 小时 |
| 9 | 8 小时 |
| 10 及以上 | 1 天，此后每次翻倍（2 天、4 天、8 天……），最长 365 天 |

- 计数按**来源地址 + 用户名**分别累计：同一出口后面的人互不影响，别人也无法故意锁住你的账号。登录成功后该组合的计数清零。
- 计数写在 `data/login_failures.json`，**重启服务端不会清零**。删除这个文件可以清空全部计数（与直接改数据目录里的其他文件同级的操作，请谨慎）。
- 这只影响聊天客户端登录。找回密码（安全问题）与后台登录仍按 `login_attempts_per_minute` 的每分钟上限和原有退避。
- `login_attempts_per_minute` 仍然生效，作为防扫描的额外一层：同一来源每分钟最多尝试几次。

### 环境变量覆盖

以下变量优先于配置文件，便于容器部署：

```
LINKCHAT_LISTEN      LINKCHAT_DATA_DIR     LINKCHAT_LOG_LEVEL
LINKCHAT_TLS_CERT    LINKCHAT_TLS_KEY      LINKCHAT_TRUST_PROXY
LINKCHAT_TRUSTED_PROXY_CIDRS   # 多个 CIDR 用逗号分隔
LINKCHAT_MIN_CLIENT_VERSION                LINKCHAT_ADMIN_ENABLED
```

## TLS 的两种方式

**方式一：服务端直连 TLS。** 同时提供证书与私钥，服务端只接受加密连接。

```json
{ "tls_cert": "/etc/link-chat/fullchain.pem", "tls_key": "/etc/link-chat/privkey.pem" }
```

证书文件不存在或与私钥不匹配时服务端拒绝启动，并在错误里指明是哪一个文件。

本机运行使用默认的 `127.0.0.1:8100`。局域网使用时显式填写服务端的局域网地址，例如 `192.168.1.20:8100`。已有配置显式填写的监听地址继续生效。

**方式二：反向代理终止 TLS。** 服务端明文监听，由 nginx/Caddy 转发。此时必须设置 `trust_proxy: true`，否则服务端看不到真实来源地址，限流会把所有流量算作同一个来源：

```nginx
location /ws {
    proxy_pass http://127.0.0.1:8100;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 120s;
}
```

只有直接连接服务端的代理地址匹配 `trusted_proxy_cidrs` 时，服务端才采信转发头。同机代理可用默认网段。代理在其他主机或容器时，填写该代理的实际地址范围。公网部署可让服务端只监听内部地址，由反向代理终止 TLS。入口代理应覆盖来自客户端的转发头，由受信代理链补充后续地址。服务端从右向左跳过受信代理，并以第一个非受信地址进行限流。

## 数据目录布局

```
data/
├── linkchat.db               账号、令牌、会话、成员、消息、设置、处分、审计（SQLite）
├── linkchat.db-wal           SQLite 预写日志，运行中存在
├── linkchat.db-shm           预写日志的共享索引，运行中存在
├── login_failures.json       登录失败计数（按来源与账号，服务端重启后仍生效）
└── server.lock               运行中的服务端持有的锁，进程退出即释放
```

除登录失败计数外，全部数据在一个 SQLite 数据库里（`modernc.org/sqlite`，纯 Go 实现，构建不需要 C 编译器），以 WAL 模式运行：

- 每个写操作是一个事务，发一条消息就是插入一行并递增该会话的序号，两者在同一事务里提交。进程在任意时刻崩溃，数据库里要么有这条消息和它的序号，要么都没有。
- 读与写互不阻塞：搜索或翻历史时，发消息照常进行。写操作之间排队，同一时刻只有一个写者。
- 历史消息留在磁盘上按需读取，进程内存不随消息量增长。
- 消息正文另有一个 trigram 全文索引，供管理后台的跨会话搜索使用。

密码哈希、令牌哈希等敏感字段与之前一样只存哈希；数据库文件本身不加密，权限依赖数据目录所在的文件系统。

## 容量与性能

在本机（Intel Core Ultra 7 265K，Windows 11）实测（`go test ./pkg/storage/ -bench .`）：

| 操作 | 条件 | 耗时 |
|---|---|---|
| 发送一条消息 | 会话已有 0 / 1,000 / 10,000 / 100,000 条 | 约 0.21–0.28 ms，与历史长度无关 |
| 翻一页历史（50 条） | 会话有 100,000 条 | 约 0.08 ms |
| 按 ID 查一条消息 | 共 100,000 条 | 约 0.01 ms |
| 打开数据库 | 共 100,000 条 | 约 1.6 ms，不随消息量增长 |
| 跨会话搜索一批 | 100,000 条，关键词 ≥3 个字符 | 约 0.1 ms（走全文索引） |
| 跨会话搜索一批 | 100,000 条，关键词 1–2 个字符 | 约 15 ms（索引帮不上，逐条匹配） |

写操作同一时刻只有一个在执行，按上表估算单实例写入上限在每秒数千条量级，对几十到几百人的团队绰绰有余。读与写互不阻塞，搜索不会拖慢发消息。

**持久性边界：** 数据库以 `synchronous=NORMAL` 运行。进程崩溃或被杀不会丢已确认的消息；但**操作系统崩溃或断电**时，最后几毫秒内已确认的写入可能丢失（数据库本身不会损坏）。

**何时该换存储：** 需要多机部署、或者写入量持续超过上述量级时。`Storage` 接口是唯一的替换点，业务层不动。

## 运维

**优雅关停。** 收到 `SIGINT` / `SIGTERM` 后：停止接受新连接 → 向所有客户端推送 `server.shutdown` 事件 → 等待在途请求完成（上限 `shutdown_grace_seconds`）→ 退出。已向发送方确认的消息在关停后必然存在于磁盘上。

**心跳与断线判定。** 客户端每 `heartbeat_seconds / 2` 主动发一个 `presence.ping`，服务端回 `pong`；任何合法帧都会重置读超时。WebSocket 的 Ping 控制帧在库内部处理，**不会**重置这个超时，所以心跳必须是应用层请求——这也是客户端必须升级到带心跳的版本的原因：旧客户端空闲 60 秒就会被断开。

**单会话登录。** 同一账号只保留一个有效会话。建立新会话的**任何**途径都会顶号，包括客户端用已保存令牌自动恢复（`auth.resume`）——不只是显式登录。服务端会吊销该账号的全部旧会话令牌并关闭旧连接，旧连接在关闭前收到 `session.replaced` 事件，其中带顶号方的来源地址与设备名。

恢复会话时令牌会**轮换**：被出示的旧令牌立即作废，新令牌返回给客户端。这是必要的，否则被顶掉的一端拿着仍然有效的旧令牌重连，就会把对方顶回去，两端反复互踢。客户端因此必须保存响应里的新令牌。

`reset` 令牌不受影响，找回密码流程不会被登录打断。

**封禁与禁言。** 管理员可以封禁或禁言账号（见下文「管理后台」）。客户端能看到的变化：

| 类型 | 名称 | 含义 |
|---|---|---|
| 错误码 | `account_banned` | 账号处于封禁中。登录（密码正确时）与恢复会话返回它，`data` 为 `{reason, expires_at}`，`expires_at` 为毫秒时间戳、`0` 表示永久。密码错误时仍返回 `invalid_credentials`，找回密码返回 `cannot_recover`，都不暴露封禁 |
| 错误码 | `muted` | 账号处于禁言中。发消息、编辑消息、建频道返回它，`data` 为 `{expires_at}`。删除自己的消息、读历史、加入或离开频道不受影响 |
| 事件 | `account.banned` | 在线时被封禁，`{reason, expires_at}`。在连接关闭之前送达 |
| 事件 | `account.muted` | 被禁言，`{reason, expires_at}` |
| 事件 | `account.unmuted` | 禁言被撤销。自然到期时不推送，客户端按 `expires_at` 自行恢复 |

被管理员强制下线或重置密码时，客户端收到的是普通的 `session.replaced`，来源显示为「管理员 / administrator」。

**客户端版本。** 客户端在握手时上报自己的版本（`hello` 的 `client` 字段，形如 `link-chat-tui/1.0.0`），服务端记在连接上，并随会话令牌一起保存。登录、恢复会话、顶号的日志都带 `client=`；不上报版本的旧客户端记为 `client=unknown`。

淘汰旧客户端的推荐做法：先发布新版本、等用户都升级，再把 `min_client_version` 设为该版本并重启服务端。被拒的新客户端会停止重连并提示升级；还不认识 `client_outdated` 的旧客户端会把它当作连接失败，按退避（最长 30 秒）反复重试，日志里能看到对应的 `client outdated` 记录。

**日志。** 每个事件一行：时间、级别、事件、`键=值`。

```
14:02:11 INF starting link_chat server version=1.0.0 min_client_version=none data_dir=/srv/link-chat/data heartbeat=30 max_message_bytes=4000
14:02:11 INF listening addr=:8100 tls=false
14:02:31 INF registered user=alice addr=10.0.0.5:51022
14:02:31 INF login ok user=alice addr=10.0.0.5:51022 device=laptop client=1.0.0
14:02:31 INF settings sent user=alice addr=10.0.0.5:51022 saved=false
14:02:32 INF settings saved user=alice addr=10.0.0.5:51022 keys=12
14:05:40 INF session displaced user=alice new_remote=10.0.0.9:40112 device=desk client=1.0.0 count=1
14:06:02 WRN client outdated addr=10.0.0.8:33010 client=unknown min=1.0.0
14:09:03 WRN settings rejected user=bob addr=10.0.0.7:60211 err="setting \"bubble_width\": invalid value \"55\""
14:10:15 INF offline user=alice addr=10.0.0.5:51022
14:11:00 ERR storage error err="…"
```

级别为 `DBG`、`INF`、`WRN`、`ERR`，由 `log_level` 控制输出到哪一级。输出到终端时，`WRN` 为黄色，`ERR` 为红色，`INF` 为绿色，时间和字段名为灰色；重定向到文件或管道、或设置了环境变量 `NO_COLOR` 时不带颜色。`info` 级记录的事件：

| 事件 | 含义 |
|---|---|
| `registered` | 新账号注册，带来源地址 |
| `login ok` | 用密码登录成功，带来源地址、设备名与客户端版本 |
| `session resumed` | 用已保存的令牌恢复登录，带客户端版本 |
| `logout` | 主动退出登录 |
| `offline` | 已登录的连接断开 |
| `session displaced` | 新登录顶掉旧会话，带新登录的来源地址与设备名 |
| `settings sent` | 客户端下载账号设置；`saved=false` 表示该账号还没有保存过设置 |
| `settings saved` | 客户端保存账号设置，`keys` 为本次修改的项数 |
| `login failed` | 密码错误 |

`warn` 级另有 `client outdated`：客户端版本低于 `min_client_version` 或未上报版本，握手被拒，带来源地址、客户端版本与最低版本。

未登录连接的建立与断开只在 `debug` 级记录。日志中不含密码、令牌原文或消息正文。

**备份。** 停止服务后复制整个数据目录即可（`linkchat.db` 连同 `-wal`、`-shm` 一起复制，或者只复制正常关停后的 `linkchat.db`）。服务运行中**不要**直接复制数据库文件，那样可能得到不一致的快照；需要在线备份时，用 `sqlite3 data/linkchat.db "VACUUM INTO 'backup.db'"` 生成一致副本。

**密码与令牌。** 密码用 Argon2id（m=64MiB, t=3, p=2）加盐哈希，参数变更后用户下次登录会自动重哈希。令牌是 32 字节随机值，磁盘上只存哈希，因此泄露数据文件不等于泄露会话。

### 实况仪表盘

在交互终端启动服务端时，默认显示实况仪表盘。顶部显示版本、在线人数、用户总数和运行时长。用户区只显示用户名，灰色代表离线，绿色代表在线，登录越久绿色越深。新消息出现时，连线从发送者延伸到接收者或频道，短暂停留后从发送端收回。终端省略消息正文。

顶部右侧时钟显示本地时间，精确到秒。底部的消息、社交、账号和系统四栏各显示自本次启动以来的累计条数与最近最多六次操作。系统栏也收录技术日志，长条目自动折行。

按 `F1` 从左侧展开快捷菜单，用上下键选择，按 `Enter` 执行。可退出、展开系统日志、暂停画面、按分类或级别筛选近期日志、查看在线连接和最近两分钟的活跃频道，也可开关动画或切换中英文。暂停只定格画面，服务端继续处理活动。切换视图时恢复实时显示。语言和动效设置在本次运行期间有效，日志原文保留写入时的语言。

日志展开后，同一菜单项变为「收起系统日志」，也可以用 `S` 直接切换。详情页可用上下键或翻页键滚动，按 `Esc` 返回实况。在实况按 `Esc` 或 `Ctrl+C` 打开关停确认，确认后优雅关停并恢复终端。

需要直接查看普通日志时，用 `./build/link-chat-server -plain` 启动。标准输出重定向到文件或管道、以及作为非交互服务运行时也使用普通日志。

## 管理后台

服务端在同一个监听地址的 `/admin/` 提供浏览器管理后台，页面随二进制发布，不需要另外部署。

### 第一次使用

后台用聊天账号登录，但只有持有管理权限的账号能进。第一个管理员——**超级管理员**——只能在服务端停止时用命令行创建：

```bash
./build/link-chat-server -config server.json -data ./data -create-admin root
# New password:      （输入时不回显）
# Repeat password:
# created super administrator root
```

- 用户名不存在时创建账号；已存在时把它提升为超级管理员，**不改密码**。
- 服务端正在运行时命令会拒绝执行并提示先停止——同一时刻只有一个进程能写数据目录（`server.lock`）。进程异常退出时操作系统会自动释放这把锁，不会卡住下次启动。
- 输入也可以从管道读取，每行一个，便于脚本化：`printf 'pw\npw\n' | link-chat-server -create-admin root`。

然后启动服务端，浏览器打开 `http://<地址>:8100/admin/` 用 `root` 登录。

超级管理员忘记密码时（它没有安全问题，不能自助找回），同样停服后执行：

```bash
./build/link-chat-server -config server.json -data ./data -reset-password root
```

这会作废该账号的全部令牌。`-reset-password` 对任何账号都可用。

### 权限

| 权限 | 后台区域 | 能做什么 |
|---|---|---|
| `stats.view` 查看统计 | 概览、实况 | 服务端版本、运行时长、连接与在线用户、各类计数与日志，以及实时活动的 3D 视图 |
| `chat.view` 查看聊天 | 会话、搜索、动态 | 浏览全部频道和**私聊**的消息，跨会话按关键词、作者、会话、时间搜索；浏览全部朋友圈动态与评论（包括「仅自己可见」的） |
| `users.moderate` 处置用户 | 用户 | 用户列表与详情（会话来源、处分历史），封禁、禁言、解除、重置密码、强制下线 |
| `accounts.manage` 管理账号 | 账号、审计 | 建号、分配权限、查看审计日志 |

规则：

- 超级管理员持有全部权限，任何人都不能降低它的权限、封禁、禁言、重置密码或强制下线。
- 任何人都不能修改自己的权限；只能授予自己持有的权限；只有超级管理员能授予或收回 `accounts.manage`。
- 每个请求都按账号**当前**的权限与封禁状态鉴权：收回权限或封禁对下一个请求立即生效，已打开的实时连接也会被断开。页面上隐藏入口只是方便，服务端对每个接口都单独检查。
- 后台建的账号没有安全问题，忘记密码只能由管理员重置。
- **删除动态或评论**需要同时持有 `chat.view` 和 `users.moderate`。删除前要确认，删除后所有人的界面上立即消失，并记入审计日志（含作者和被删除的正文）。

### 实况

持有 `stats.view` 的管理员可以从导航打开「实况」。在线用户对应发光星体，频道以星环表示，新消息以流星展示发送方向。侧栏显示在线用户、频道数、今日消息、近一分钟消息和分类活动。消息计数以 SQLite 中留存的消息记录为准，包括频道加入等系统提示。

星环上的 `2/10` 表示 10 名成员中有 2 人在线。星环按成员数相对缩放。持有 `chat.view` 时，选中星环可查看成员，选中玩家可查看他加入的频道和好友连线。使用新版客户端打开频道后，星体飞入对应星环；切换页面或断线时飞离，频道消息在星体抵达后播放。旧客户端仍可聊天，但无法提供当前查看的频道状态。

同时持有 `chat.view` 时，流星可以附上消息正文，频道星环显示名称；仅有 `stats.view` 时，频道以匿名星环显示。场景和所需脚本均由服务端提供，可在断开外网时运行。开启系统或浏览器的减少动态效果偏好后，页面减少镜头和粒子动画。浏览器无法启动 3D 场景时，侧栏照常显示统计和活动。

### 封禁与禁言

- **封禁**立即断开对方所有聊天连接和后台连接、作废其全部登录，此后登录、恢复会话与找回密码都被拒绝。客户端会显示原因与解封时间。
- **禁言**期间可以登录和阅读，但不能发消息、编辑消息、建频道、发动态、评论或修改主页资料；点赞和删除自己的内容不受影响。
- 两者都必须填写原因；时长可选 1 小时、1 天、7 天、30 天、自定义或永久。到期自动失效，不需要人工解除。
- 对同一个人再次封禁（或禁言）会取代仍在生效的那一次，旧记录标为「被新处分取代」。所有记录永久保留，在用户详情里可以看到。
- **强制下线**不是处分：对方立即下线，但可以马上重新登录。

### 审计

以下操作写入审计日志（数据库 `audit` 表），持有 `accounts.manage` 的管理员可以在「审计」里按操作人、操作类型与时间筛选：

后台登录与退出 · 查看某个会话的消息（私聊和频道都记，往前翻页也记） · 每次搜索（含全部条件） · 封禁、禁言、解除 · 重置密码、强制下线 · 建号、修改权限 · 命令行创建、提升超级管理员、重置密码（操作人记为 `cli`） · 删除动态、删除评论

审计日志只追加，后台没有删除或修改的途径，也不记录密码或令牌。聊天客户端里**不提示**管理员可以查看消息，这是有意的：约束靠审计，而不是靠告知。

### 后台的会话

- 后台登录得到的是一个专用令牌，存在 HttpOnly、SameSite=Strict、仅限 `/admin/` 路径的 cookie 里，有效期 12 小时。HTTPS 下（直连 TLS，或开了 `trust_proxy` 且代理传来 `X-Forwarded-Proto: https`）带 `Secure`。
- 后台会话与聊天会话互不影响：登录后台不会顶掉同一账号的聊天客户端，反之亦然；两种令牌也不能互相冒用。
- 后台登录与聊天登录共用同一个失败退避，按来源主机计数。
- 所有写操作都要求 `X-Requested-With: link-chat-admin` 头和 JSON 请求体，其他网站的页面无法代替管理员发出请求。

### 公网部署

后台与聊天共用端口。公网部署时建议：

- 不需要后台的实例设置 `admin_enabled: false`。
- 用反向代理限制 `/admin/` 的来源，例如 nginx：

  ```nginx
  location /admin/ {
      allow 10.0.0.0/8;     # 办公网或 VPN
      deny  all;
      proxy_pass http://127.0.0.1:8100;
      proxy_buffering off;  # 后台的实时事件流（SSE）需要
      proxy_read_timeout 1h;
  }
  ```

- 启用 TLS，让后台 cookie 带上 `Secure`。

### 搜索的代价

搜索在存储内顺序扫描，每次最多扫描 2 万条、返回 100 条，未扫完时页面上出现「继续搜索」。这让单次搜索对发消息的影响限制在毫秒级。消息总量到百万级时，应换成带全文索引的存储后端（`Storage` 接口不变，后台不受影响）。

## 故障排查

| 现象 | 原因 |
|---|---|
| 启动即退出，提示 `listen is required` | 配置缺少必填项 |
| 启动即退出，提示 `data_dir is not writable` | 数据目录权限不足 |
| 客户端全部报 `rate_limited` | 反向代理后没开 `trust_proxy`，所有请求算作同一来源 |
| 客户端频繁断线重连 | 反向代理的读超时短于心跳周期，调大 `proxy_read_timeout` |
| 启动即退出，提示 `min_client_version` 格式不合法 | 该项必须是 `x.y.z`，不能带 `v` 前缀或后缀 |
| 部分客户端连不上，日志里有 `client outdated` | 这些客户端低于 `min_client_version`，需要升级 |
| 启动即退出，提示 `is in use by another link-chat-server` | 已有服务端（或 `-create-admin` / `-reset-password`）在使用同一个数据目录 |
| 打开 `/admin/` 是 404 | 配置了 `admin_enabled: false`，或环境变量 `LINKCHAT_ADMIN_ENABLED=false` |
| 后台登录提示「没有权限」 | 账号没有任何管理权限；用 `-create-admin` 或让管理员授权 |
| 后台的实时数据不更新 | 反向代理缓冲了 `/admin/api/events`，对该路径关闭 `proxy_buffering` |
| `/healthz` 返回 503 | 进程正在关停，或 SQLite 在探测超时内无法读取数据库 |
| 重启后消息缺失 | 只有已向发送方确认的消息保证存在；未确认的发送在进程被杀时会整体回滚，客户端重连后会重发 |
