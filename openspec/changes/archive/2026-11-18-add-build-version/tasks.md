# Tasks

## 1. 版本来源与解析

- [x] 1.1 新增根目录 `VERSION`（`0.0.0`）与 `version.go`（包 `linkchat`，`//go:embed VERSION` 导出 `RawVersion`）；验证：`go build ./...` 通过
- [x] 1.2 新增 `pkg/version`：`Parse`、`Compare`、`Current`、`String`、`Valid`、`ClientTag`、`ParseTag`；验证：表驱动测试覆盖 spec 中的合法与非法版本号、`0.10.0 > 0.9.5`、相等、`ParseTag` 对缺失与无版本的标签返回 false，并有一条测试断言仓库 `VERSION` 合法
- [x] 1.3 删除 `pkg/app/version.go`，关于页改用 `version.String()`；Makefile 删除 `VERSION` 注入相关变量与注释；两个 `main.go` 增加 `-version`；验证：现有关于页快照测试更新后通过，`go run ./cmd/link-chat -version` 与 `go run ./cmd/link-chat-server -version` 输出同一版本

## 2. 协议与服务端

- [x] 2.1 `pkg/protocol` 新增 `CodeClientOutdated = "client_outdated"` 及中英文案、载荷 `ClientOutdatedData{MinVersion, ClientVersion}`；验证：协议测试断言错误码不与已有字符串重复
- [x] 2.2 `Config` 新增 `min_client_version` 与 `LINKCHAT_MIN_CLIENT_VERSION`，`Validate` 校验格式，生成的默认配置包含该项注释；启动日志打印 `version` 与 `min_client_version`；验证：配置测试覆盖未设置、合法、`v0.3` 被拒、环境变量覆盖
- [x] 2.3 `handleHello` 解析 `client` 标签记入 `Conn.clientVersion`，`hello-ack` 返回 `server` 标签；设置了最低版本时对过旧、缺失、不合法的版本返回 `client_outdated` 并关闭连接、记 WRN 日志；验证：服务端测试覆盖低于、等于、高于最低版本，未设置最低版本时旧客户端照常握手，`hello-ack` 中 `server` 可被 `ParseTag` 解析
- [x] 2.4 `storage.Token` 新增 `ClientVersion`，签发与恢复会话时写入；`login ok`、`session resumed`、`session displaced` 日志带 `client=`；验证：存储测试往返该字段，服务端测试断言令牌中的版本与日志输出
- [x] 2.5 更新 `docs/server.md`：`min_client_version` 配置项、`-version`、`client_outdated` 错误码；验证：文档配置表包含新项

## 3. 客户端

- [x] 3.1 `Transport.handshake` 发送 `ClientTag("link-chat-tui")`，收到 `client_outdated` 返回 `*OutdatedError`；验证：传输层测试对假服务端断言 `hello` 中的 `client` 字段与错误类型
- [x] 3.2 `onConnectFailed` 识别 `OutdatedError`：停止重连、回到登录界面、错误行显示当前与最低版本，保留用户名与已保存的令牌；补中英文案；验证：e2e 测试（真实服务端设置较高的最低版本）断言登录时与断线重连时都显示提示且不再重试，文案两种语言下不超出面板
- [x] 3.3 更新 `docs/client.md` 与 README 的「开发」一节：`-version`、发版流程（修改 `VERSION` 并提交）、版本过旧提示；验证：README 中不再出现 `make build VERSION=`

## 4. 整合

- [x] 4.1 `gofmt`、`go vet`、`go test ./...`、`openspec validate add-build-version --strict` 全部通过；手动：`make build` 后两个二进制 `-version` 输出一致，服务端设 `min_client_version` 为 `9.0.0` 时客户端登录显示过旧提示

## 5. 版本号

- [x] 5.1 按版本规则把根目录 `VERSION` 从 `0.0.0` 改为 `1.0.0`；验证：`go test ./pkg/version/...` 通过，两个二进制 `-version` 输出 `1.0.0`

## Workflow follow-up

- 用户确认后归档：`openspec archive add-build-version`，再 `openspec validate --all --strict`。
- 归档后更新 `add-admin-console`：用户详情、在线列表、会话来源显示客户端版本，概览显示在线客户端版本分布。
