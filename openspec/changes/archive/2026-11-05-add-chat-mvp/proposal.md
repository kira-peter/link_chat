# Proposal

## Why

目前缺少一个既能真正在终端里舒服使用、又足够好看的多用户聊天工具。现有终端 IM 要么停留在 ncurses 时代的粗糙排版，要么把复杂度压给用户（自建 XMPP、配置 bot）。同时，Claude Code 的 `/config` 界面证明了一件事：终端界面完全可以在对齐、留白、色彩层级和动效上做到专业桌面软件的质感，而且不需要 GUI 框架。

`link_chat` 要把这件事做出来：一个服务端 + 一个终端客户端的聊天系统，界面克制、细节讲究，用户打开就能聊，关闭后消息仍在。

## What Changes

**新增一个完整的聊天系统，包含服务端二进制与终端客户端二进制。**

- **服务端**：单进程、可公网部署。WebSocket 接入、账号注册与登录、频道与私聊、消息广播与历史持久化、在线状态、心跳与断线回收。
- **客户端**：全屏终端 TUI。会话列表 + 消息流 + 输入框的三栏式布局，支持频道切换、私聊、未读标记、历史回溯、主题切换、中英双语。
- **界面语言**：以 Claude Code `/config` 为参考基线——单一色彩令牌体系、统一的行高与缩进网格、克制的边框与分隔线、仅在必要处使用强调色、选中态用整行背景而非粗体、动效只用于状态反馈（连接中、发送中、新消息）。
- **协议**：WebSocket 之上定义 JSON 信封协议，带协议版本号、请求-响应关联 ID、结构化错误码。
- **持久化**：服务端以 JSON 文件落盘（用户、频道、消息、成员关系），通过 `Storage` 接口隔离，后续可整体替换为 SQLite 而不改动业务层。

**关键约束（公网多用户带来的硬要求）**

- 密码 MUST NOT 明文存储，使用 Argon2id 加盐哈希。
- 会话令牌使用随机 opaque token，服务端可吊销。
- 所有写操作必须限流，防止消息刷屏与注册滥用。
- 客户端连接使用 `wss://`；服务端支持证书文件或反向代理两种 TLS 终止方式。
- JSON 文件存储 MUST 通过单写者协程串行化写入并做原子替换，避免并发写损坏。

**明确不做（MVP 范围外）**

- 富文本 / Markdown 渲染、图片与文件传输
- 端到端加密（传输层加密由 TLS 提供）
- 移动端与 Web 客户端
- 语音、视频、已读回执

## Capabilities

### New Capabilities

- `chat-protocol`：客户端与服务端之间的 WebSocket JSON 信封协议——帧结构、版本协商、请求/响应/事件三类消息、错误码表、心跳与超时。
- `account-auth`：账号注册、登录、密码哈希、令牌签发与吊销、连接期身份校验、公网防护（限流、失败退避）。
- `chat-rooms`：频道与私聊会话的创建、加入、退出、成员查询，以及会话元数据（名称、主题、创建者、可见性）。
- `message-delivery`：消息的发送、校验、顺序保证、广播分发、投递确认，以及加入会话时的历史补发。
- `server-runtime`：服务端进程的启动配置、监听与 TLS、`Storage` 接口与 JSON 文件实现、优雅关停、日志。
- `tui-design-system`：客户端的设计令牌与基础组件——色彩令牌、间距网格、边框与分隔线、行样式、对话框、提示条、加载态，以及终端能力降级规则。
- `tui-chat-client`：客户端界面与交互——三栏布局、会话列表、消息流渲染与折行、输入框与编辑键位、未读与在线状态、状态栏与快捷键、主题与语言切换。

### Modified Capabilities

无。这是新工程，`openspec/specs/` 目前为空。

## Impact

**新建代码**

- 模块：`link-chat`（Go 1.26+），单一仓库双二进制。
- `cmd/link-chat-server/`、`cmd/link-chat/`：两个入口。
- `pkg/protocol/`：协议定义与编解码，服务端与客户端共享。
- `pkg/server/`：连接管理、路由、限流、认证、存储。
- `pkg/storage/`：`Storage` 接口与 JSON 文件实现。
- `pkg/tui/`：渲染层、布局、组件、主题令牌。
- `pkg/client/`：连接状态机、重连、本地会话状态。
- `pkg/i18n/`：中英文文案。

**依赖**

- `github.com/gorilla/websocket` 或 `github.com/coder/websocket`：WebSocket 收发。
- `github.com/gdamore/tcell/v2`：终端渲染（与 `claude_manager` 保持一致的技术选型，便于复用心得）。
- `github.com/mattn/go-runewidth`：CJK 与 emoji 宽度计算，决定折行与对齐的正确性。
- `golang.org/x/crypto/argon2`：密码哈希。
- 标准库 `net/http`、`encoding/json`、`crypto/rand`。

**运维面**

- 服务端需要一个数据目录与一个配置文件；首次启动自动生成默认配置与管理员引导信息。
- 客户端需要一个配置文件记录服务器地址、令牌、主题、语言。

**风险**

- 公网 + JSON 文件存储的组合在并发写和规模增长后会成为瓶颈；本提案用单写者协程与存储接口把影响限制在 `pkg/storage`，但真正的多机部署需要先换 SQLite 或 Postgres。
- 终端渲染在不同终端模拟器（Windows Terminal / iTerm2 / VSCode 终端）上的 Unicode 边框与色彩表现不一致，`tui-design-system` 需要显式定义降级路径。
