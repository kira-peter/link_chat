# Design

## Context

动机见 proposal.md。现状：

- **构建版本**：`pkg/app/version.go` 定义了 `var Version = "dev"`，只能在 Makefile 里通过 `-ldflags -X` 注入，也只有关于页在用。服务端完全没有版本概念。
- **握手协议**：`hello` 和 `hello-ack` 的载荷 `HelloPayload` 已经有 `Client` 和 `Server` 两个字段（`pkg/protocol/frame.go:50`），目前分别固定写成 `"link-chat-tui"` 和 `"link-chat"`。服务端完全不读这两个字段。
- **握手失败的处理**：客户端在 `Transport.handshake` 里收到 `TypeError` 后，会把它变成 `fmt.Errorf("%s: %s", code, msg)` 返回，再由 `onConnectFailed` 进入退避重连。
- **令牌记录**：令牌已经记录了 `Remote`、`Device`、`LastSeenAt`，后台展示「会话来源」时会用到这些字段。

## Goals / Non-Goals

**Goals:**
- 不管用什么方式构建，都能得到同一个正确的版本号。
- 版本只存在于一处，发版只需改一个文件。
- 握手阶段就完成版本判定，过旧的客户端连登录请求都发不出来。

**Non-Goals:**
- 不做自动更新，也不做下载地址的分发。
- 不支持预发布后缀（`-beta`、`+build`）和 git 提交哈希。
- 不改协议版本号 `version: 1`。
- 不做「低于推荐版本只提示」的软限制。

## Decisions

### 1. `VERSION` 文件放在仓库根目录，由根目录的小包嵌入

`go:embed` 只能嵌入本包目录及其子目录里的文件，`pkg/version` 拿不到 `../../VERSION`。因此做如下安排：

- 仓库根目录放 `VERSION`，实现开始时内容为 `0.0.0`，本变更的最后一步按版本规则改为 `1.0.0`。
- 根目录新增 `version.go`，包名 `linkchat`，导入路径 `link-chat`。文件里只有一行 `//go:embed VERSION` 和一个导出的字符串变量 `RawVersion`。
- 新增 `pkg/version`，负责去掉首尾空白、解析成 `Version{Major, Minor, Patch}`，并对外提供：
  - `version.Current()`：返回 `Version`。
  - `version.String()`：返回 `"1.0.0"`。
  - `Parse(s) (Version, error)`。
  - `Compare(a, b) int`。
  - `ClientTag(name)`：返回 `"<name>/<x.y.z>"`。
  - `ParseTag(s) (name string, v Version, ok bool)`。
- `pkg/version` 在包初始化时解析 `RawVersion`。解析失败时不 panic，`Current()` 返回零值，`Valid()` 返回 false。
- `pkg/version` 的测试断言 `Valid()` 为真。这样 `VERSION` 写错时 `go test ./...` 会失败，但不会让运行中的二进制崩溃。

**备选方案**：

- 把 `VERSION` 放进 `pkg/version/` 目录。这样不用在根目录建包，但发版时要去子目录里找文件，不够直观。用户要的是根目录的一个 VERSION 文件。
- 继续用 ldflags 注入，或者取 `git describe`。用户已经否决：直接 `go build` 时会拿到 `dev`，而且依赖构建环境。

### 2. 复用 `HelloPayload` 已有字段，不改帧结构

两端发送的内容：

- 客户端发送 `Client: version.ClientTag("link-chat-tui")`。
- 服务端回复 `Server: version.ClientTag("link-chat")`。

两个字段原本就是可选的，旧端会忽略其中的内容，所以协议版本保持为 1。

服务端用 `ParseTag` 解析客户端发来的字段：

- 能解析：版本记到 `Conn.clientVersion`。
- 不能解析：记为空串，表示「未知」，日志和后台里显示为 `unknown`。

### 3. 版本检查放在 `handleHello` 里

在 `handleHello` 的协议版本检查之后，判断 `cfg.MinClientVersion`：

- 不为空，且客户端版本未知或低于它时，用 `writeThenClose` 回一个 `client_outdated` 错误帧。
- 错误帧的 `data` 为 `{min_version, client_version?}`，并记一条 `WRN client outdated` 日志。

这个检查必须在 `helloDone=true` 之前执行，所以连接上不会发生任何认证。

配置的处理：

- `Config.Validate()` 用 `version.Parse` 校验配置项，解析后的值缓存在 `Config` 上。
- 环境变量为 `LINKCHAT_MIN_CLIENT_VERSION`。

### 4. 客户端把 `client_outdated` 当作终止性错误

`Transport.handshake` 识别到 `client_outdated` 时，返回带类型的 `*OutdatedError{Min, Current}`，不再返回普通字符串错误。

`onConnectFailed` 用 `errors.As` 识别这个错误，然后：

1. 把状态设为 `StateAuthFailed`。
2. 不安排重连。
3. 打开登录舞台，在错误行写入 `i18n.ClientOutdated` 格式化后的文本。

登录面板上的「连接后登录」路径（`dialogs.go:185`）和自动重连路径都会走 `onConnectFailed`，所以只需改这一处。

令牌不清除：升级之后，用户应当能直接用已保存的会话恢复。

### 5. 令牌与日志记录客户端版本

- `storage.Token` 新增字段 `ClientVersion string \`json:"client_version,omitempty"\``，在 `issueSession` 和恢复会话时写入。
- `login ok`、`session resumed`、`session displaced` 三条日志都增加 `client=<版本>`。
- 服务端启动日志增加 `version` 和 `min_client_version`。

### 6. `-version` 参数和清理旧机制

- 两个 `main.go` 都增加 `-version`，打印 `link-chat-server 1.0.0` 或 `link-chat 1.0.0` 后退出。
- 删除 `pkg/app/version.go`。关于页改为读取 `version.String()`。
- Makefile 删除 `VERSION ?=`、`VERSION_PKG` 和 `-X`，只保留 `-s -w`，并删掉相关注释，避免有人以为 `make build VERSION=` 仍然有效。

### 7. 顺带修复：自动重连从未真正连上

实现「断线重连时版本过旧」的端到端测试时发现了一个已有缺陷：`tryReconnect` 用 `context.WithTimeout` 建了一个上下文，并在函数返回时 `defer cancel()`。但 `connect` 只是在 goroutine 里启动拨号就立即返回，所以每次重连的上下文都在拨号前被取消，客户端永远停在「已断开 / 重连中」。

修复方式：`App` 保存 `Run` 收到的上下文，重连拨号挂在它上面。退出程序时它会被取消，其他时候不会。这个缺陷不影响规格，因为规格本来就要求断线后自动重连；本变更的重连测试同时覆盖了它。

### 8. 顺带修复：重连成功后清除「服务端正在重启」

第 7 条修好重连之后，又暴露出一个问题：

- 收到 `server.shutdown` 时，状态栏会写入「服务端正在重启」，但之后没有任何地方清除它。
- 红色提示在状态栏里的优先级最高，所以重连成功后，这条提示仍然一直显示。

修复方式：写入这条提示时，打上「属于上一条连接」的标记。重新登录成功时，只清除带这个标记的提示。在它之后出现的其他提示（例如「设置已保存」）不受影响。

这个修复同样不改变规格，因此不单独递增版本号，随本变更一起作为 `1.0.0` 发布。

## Risks / Trade-offs

- **发版时忘了改 `VERSION`** → 两次发布的版本号相同，后台无法区分。缓解办法：README 写明发版流程；版本号本身没有安全含义，出错的代价是可见但可控的。
- **旧客户端遇到 `client_outdated` 会无限退避重试** → 旧客户端无法改变。退避上限是 30 秒，对服务端的压力可以忽略；服务端日志会持续记录被拒的来源，管理员可以据此通知用户。
- **从工作区构建的「开发版」和正式版同号** → 这是有意的取舍，换来的是不依赖构建环境。需要区分时，修改 `VERSION` 即可。

## Migration Plan

- 首次发版为 `1.0.0`，`min_client_version` 保持为空。
- 所有客户端升级到 1.0.0 之后，运维可以把 `min_client_version` 设为 `1.0.0`，从而淘汰不上报版本的旧客户端。
- 回滚方式：换回旧二进制即可。令牌中多出的 `client_version` 会被旧版本忽略。
