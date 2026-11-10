# Tasks

## 1. 协议与存储

- [x] 1.1 `RegisterReq` 增加 `security_question` 与 `security_answer`；新增 `RecoverQuestionReq/Res`、`RecoverVerifyReq/Res`、`RecoverResetReq` 载荷结构，验证编解码单测覆盖每个结构
- [x] 1.2 新增操作名 `auth.recover.question|verify|reset` 与错误码 `security_question_required`、`security_answer_incorrect`、`reset_token_invalid`、`reset_token_expired`、`cannot_recover`，验证错误码唯一性测试通过
- [x] 1.3 `storage.User` 增加 `SecurityQuestion` 与 `SecurityAnswerHash`；`storage.Token` 增加 `Purpose`（空值按 `session` 处理），验证旧数据文件缺少这些字段时仍能正常加载
- [x] 1.4 存储测试覆盖：新字段持久化往返、`Purpose` 过滤、老数据兼容，验证 `go test ./pkg/storage/` 通过

## 2. 服务端

- [x] 2.1 实现答案归一化（小写、压缩空白）与哈希，验证归一化单测覆盖多空格、大小写、首尾空白
- [x] 2.2 注册处理器要求并校验安全问题与答案，验证缺少问题时返回 `security_question_required`、答案落库为哈希而非明文
- [x] 2.3 实现 `auth.recover.question`：返回问题文本；用户不存在与无问题统一返回 `cannot_recover`，验证两者响应码与文案完全一致
- [x] 2.4 实现 `auth.recover.verify`：校验答案，签发 5 分钟一次性重置令牌，验证错误答案不计入签发、正确答案签发的令牌 `Purpose` 为 `reset`
- [x] 2.5 实现 `auth.recover.reset`：校验令牌、更新密码哈希、删除令牌、吊销该用户全部会话令牌，验证重置后旧会话令牌被拒、其他账号不受影响
- [x] 2.6 找回流程接入限流：答案错误计入与登录共用的退避，验证连续错误答案触发 `rate_limited`
- [x] 2.7 令牌校验区分用途，验证重置令牌不能用于 `auth.resume`、会话令牌不能用于 `auth.recover.reset`

## 3. 客户端状态与配置

- [x] 3.1 客户端默认服务器地址改为 `ws://127.0.0.1:8100/ws`，`-server` 参数与配置文件仍可覆盖，验证三级优先级
- [x] 3.2 `LoginModel` 去掉 `Server` 字段，改为焦点区域枚举（用户名 / 密码 / 登录按钮 / 忘记密码 / 立即注册），验证状态单测覆盖焦点循环
- [x] 3.3 新增 `RegisterModel`（含预置问题列表与自定义输入）与 `RecoverModel`（三步 + 步内字段），验证状态迁移单测覆盖前进与返回

## 4. 登录面板与注册表单

- [x] 4.1 重绘登录面板：用户名、密码、登录按钮、两条带下划线的链接；底部提示不再被截断，验证渲染快照中四类元素齐备且提示完整可见
- [x] 4.2 实现 Tab/Shift+Tab 在五个可交互元素间循环，焦点有明确视觉标识，验证每个焦点位置都有唯一的选中表现
- [x] 4.3 实现 Enter 激活当前焦点（提交登录 / 触发链接），验证按钮与链接分别触发正确动作
- [x] 4.4 实现鼠标点击链接：行级命中判定，验证模拟点击两条链接分别打开注册与找回
- [x] 4.5 实现注册表单：用户名、密码、安全问题（预置列表可展开或自定义）、安全答案，验证表单渲染与必填校验
- [x] 4.6 注册表单 Esc 返回登录且保留用户名，验证交互测试

## 5. 找回密码向导

- [x] 5.1 实现三步向导的界面与步进提示，验证第二步显示问题、第三步显示新密码与确认
- [x] 5.2 接通三个服务端请求，验证用户名不可找回、答案错误、令牌过期三条失败路径各自停在正确步骤并给出对应提示
- [x] 5.3 实现两次密码不一致的本地校验与焦点回退，验证不发出请求
- [x] 5.4 重置成功后关闭向导、打开登录并预填用户名，验证端到端测试

## 6. 端到端与文档

- [x] 6.1 端到端测试：注册（带问题）→ 登出 → 忘记密码 → 答对 → 重设 → 用新密码登录，验证全链路通过
- [x] 6.2 端到端测试：重置后重置前签发的令牌被拒绝，验证会话吊销生效
- [x] 6.3 更新 `docs/client.md`：登录面板说明、注册与找回流程、安全问题的作用与安全边界、改服务器地址的三条路径
- [x] 6.4 更新 `README.md` 快速开始，与新的默认地址和登录流程一致

## Workflow follow-up

- 评审通过后执行 `openspec archive add-password-recovery`。
- 归档后运行 `openspec validate --strict --no-interactive` 确认全部规格通过。
