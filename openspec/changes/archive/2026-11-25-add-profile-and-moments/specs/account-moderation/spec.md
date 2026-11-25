# Spec Delta

## MODIFIED Requirements

### Requirement: 禁言

禁言期间该账号 MUST 能登录、阅读、加入频道与删除自己的消息，但发送消息、编辑消息和创建频道 MUST 被拒绝并返回 `muted`（附带到期时间）。禁言期间发布动态、发表评论与修改主页资料 MUST 同样被拒绝并返回 `muted`；点赞、删除自己的动态与评论、处理好友申请不受影响。禁言生效时系统 SHALL 向该账号的在线连接推送 `account.muted`（附带到期时间），被撤销时推送 `account.unmuted`；自然到期时不推送，客户端按到期时间自行恢复。

#### Scenario: 禁言后发消息
- **WHEN** 被禁言的 `dave` 发送一条消息
- **THEN** 系统返回 `muted` 与到期时间，消息不被保存或广播

#### Scenario: 禁言期间可以阅读
- **WHEN** 被禁言的 `dave` 打开频道查看历史
- **THEN** 历史正常返回

#### Scenario: 撤销禁言即时通知
- **WHEN** 管理员撤销 `dave` 在线时的禁言
- **THEN** `dave` 的连接收到 `account.unmuted`，之后可以正常发消息

#### Scenario: 禁言后发动态与评论
- **WHEN** 被禁言的 `dave` 发布动态或发表评论
- **THEN** 系统返回 `muted` 与到期时间，内容不被保存

#### Scenario: 禁言后改资料
- **WHEN** 被禁言的 `dave` 修改昵称
- **THEN** 系统返回 `muted`，昵称保持原值

#### Scenario: 禁言期间可以点赞
- **WHEN** 被禁言的 `dave` 给好友的动态点赞
- **THEN** 点赞成功

### Requirement: 审计日志

系统 SHALL 为以下操作写入审计日志，每条包含时间、操作人、操作类型、对象与必要的细节：
- 后台登录与退出
- 查看某个会话的消息
- 搜索（含全部条件）
- 封禁、禁言、撤销
- 重置密码、强制下线
- 建号、修改权限
- 命令行创建或提升超级管理员
- 后台删除动态或评论（含作者与被删除的正文）

审计日志 MUST 只追加，后台中 MUST NOT 有删除或修改审计记录的途径。审计日志 MUST NOT 包含密码或令牌。

#### Scenario: 查看私聊留下记录
- **WHEN** 管理员 `bob` 打开 `alice` 与 `carol` 的私聊
- **THEN** 审计日志新增一条：`bob` 在该时间查看了这个私聊

#### Scenario: 重置密码不记录密码
- **WHEN** 管理员重置了 `erin` 的密码
- **THEN** 审计日志记录了这次重置，但不包含新密码

#### Scenario: 删除动态留下记录
- **WHEN** 管理员 `bob` 删除了 `alice` 的一条动态
- **THEN** 审计日志新增一条：`bob` 删除了 `alice` 的动态，附带该动态的正文
