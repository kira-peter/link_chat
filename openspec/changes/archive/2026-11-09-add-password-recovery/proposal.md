# Proposal

## Why

当前登录界面把服务器地址当成一个可编辑字段，对一个终端聊天客户端来说是错误的默认：绝大多数人连的是自己部署的那一台，地址属于配置而不是每次登录都要面对的输入。它还挤占了表单，导致提示文本放不下、注册入口只能藏在一条 Tab 提示里，用户找不到。

同时账号体系只有注册和登录，没有任何找回手段——忘记密码等于永久失去账号和全部聊天记录。这在自建、小团队场景里是硬伤。

## What Changes

**登录界面重构**

- 服务器地址不再出现在登录表单里。它来自客户端配置，默认 `ws://127.0.0.1:8100/ws`；需要改地址时改配置或用 `-server` 参数。
- 表单只剩用户名与密码，下面是**登录按钮**。
- 按钮下方是两个带下划线的文字链接：**忘记密码**、**立即注册**。
- 链接可用鼠标点击（终端支持时）与键盘操作；Tab 在字段与按钮、链接之间循环。

**注册需要安全问题**

- 注册表单包括：用户名、密码、安全问题（从预置列表选择或自定义）、安全答案。
- 服务端存安全答案的**哈希**（与小写归一化后的值），不存明文。
- 已存在的账号（升级前注册的）没有安全问题，登录不受影响，但不能用找回密码。

**找回密码三步流程**

1. 输入用户名 → 服务端返回该账号的安全问题。
2. 回答正确 → 服务端签发一个**短期重置令牌**（5 分钟，一次性）。
3. 用重置令牌设置新密码 → 令牌作废，该账号的其余会话令牌全部吊销。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `account-auth`：注册增加安全问题；新增找回密码的三步流程、重置令牌、答案校验与限流；密码重置后吊销该用户全部会话。
- `tui-chat-client`：登录界面改为「用户名 + 密码 + 按钮 + 两个链接」；服务器地址移出表单；新增注册表单与找回密码的三步对话框；链接可点击。

## Impact

**协议**（`pkg/protocol`）

- `RegisterReq` 增加 `security_question`、`security_answer`。
- 新增操作：`auth.recover.question`、`auth.recover.verify`、`auth.recover.reset`。
- 新增错误码：`security_question_required`、`security_answer_incorrect`、`reset_token_invalid`、`reset_token_expired`、`cannot_recover`。

**存储**（`pkg/storage`）

- `User` 增加 `SecurityQuestion`、`SecurityAnswerHash`。
- 重用 `tokens` 集合承载重置令牌（加 `Purpose` 字段区分会话令牌与重置令牌），避免新增一套存储。

**服务端**（`pkg/server`）

- 注册校验问题与答案；答案哈希复用 Argon2id。
- 三个新处理器；答案校验失败与用户名枚举共用限流预算。
- 重置成功后 `DeleteTokensForUser`。

**客户端**（`pkg/app`、`pkg/tui/views`）

- `LoginModel` 去掉 `Server` 字段，增加当前焦点区域（字段 / 按钮 / 链接）。
- 新增 `RecoverModel` 承载三步流程。
- 配置项默认值改为本机地址。

**i18n**：新增约 30 条中英文文案。

**兼容性**：旧的 `server` 字段仍从配置读取；`-server` 参数保留。老账号没有安全问题，找回时返回 `cannot_recover` 并提示联系管理员重置数据目录。**这不是 BREAKING**，登录与注册行为向后兼容（安全问题为可选字段时另议——本提案要求新注册必须提供）。
