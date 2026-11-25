# Spec Delta

## Purpose

定义账号之间的双向好友关系：如何建立、解除与拉黑，以及这种关系决定的"谁能看到谁的主页动态与互动"。

## ADDED Requirements

### Requirement: 发送好友申请

已登录用户 SHALL 能按用户名向另一个账号发送好友申请，并可附一句不超过 100 个字符的话。对自己、对已是好友的人、对不存在的用户名申请 MUST 被拒绝。向已经拉黑自己的人申请 MUST 返回与"用户不存在"相同的错误，不暴露拉黑关系。

#### Scenario: 发出申请
- **WHEN** `alice` 向 `bob` 发送附言"我是 alice"的好友申请
- **THEN** 申请成功，`bob` 的待处理申请中出现来自 `alice` 的这条申请与附言

#### Scenario: 申请自己
- **WHEN** `alice` 向 `alice` 发送好友申请
- **THEN** 系统返回 `invalid_argument`

#### Scenario: 已是好友
- **WHEN** `alice` 与 `bob` 已是好友，`alice` 再次申请
- **THEN** 系统返回 `already_friends`

#### Scenario: 被对方拉黑
- **WHEN** `bob` 已拉黑 `alice`，`alice` 向 `bob` 申请
- **THEN** 系统返回 `user_not_found`，`bob` 不会收到任何申请

### Requirement: 申请的合并与互相申请

同一对用户之间 MUST 至多有一条待处理申请。重复申请 MUST 只更新附言与时间，不产生第二条。若 `bob` 向已经向自己发出待处理申请的 `alice` 申请，系统 MUST 直接让两人成为好友。

#### Scenario: 重复申请
- **WHEN** `alice` 向 `bob` 申请两次，第二次附言为"再试一次"
- **THEN** `bob` 只有一条来自 `alice` 的申请，附言为"再试一次"

#### Scenario: 互相申请
- **WHEN** `alice` 已向 `bob` 申请且未处理，`bob` 向 `alice` 申请
- **THEN** 两人立即成为好友，这条申请不再待处理

### Requirement: 处理好友申请

收到申请的人 SHALL 能同意或拒绝。同意后两人 MUST 成为好友；拒绝后申请消失，申请人 MAY 再次申请。处理不属于自己的申请 MUST 被拒绝。

#### Scenario: 同意
- **WHEN** `bob` 同意 `alice` 的申请
- **THEN** `alice` 与 `bob` 出现在彼此的好友列表中

#### Scenario: 拒绝
- **WHEN** `bob` 拒绝 `alice` 的申请
- **THEN** 两人不是好友，`bob` 的待处理申请中不再有这条，`alice` 之后可以再次申请

#### Scenario: 处理别人的申请
- **WHEN** `carol` 试图同意 `alice` 发给 `bob` 的申请
- **THEN** 系统返回 `not_found`

### Requirement: 申请与关系变化的实时通知

好友申请到达、被同意时，系统 SHALL 向相关账号的在线连接推送事件。离线的账号 MUST 在下次登录后通过查询看到待处理申请。

#### Scenario: 在线收到申请
- **WHEN** `bob` 在线时 `alice` 向他申请
- **THEN** `bob` 的连接收到好友申请事件，含申请人与附言

#### Scenario: 离线后看到申请
- **WHEN** `alice` 在 `bob` 离线时向他申请，之后 `bob` 登录
- **THEN** `bob` 查询待处理申请时能看到这条申请

#### Scenario: 申请被同意
- **WHEN** `bob` 同意 `alice` 的申请且 `alice` 在线
- **THEN** `alice` 的连接收到好友关系建立的事件

### Requirement: 好友列表与删除好友

用户 SHALL 能查看自己的好友列表，每项含用户名、昵称、头像、是否在线。用户 SHALL 能删除好友，删除对双方同时生效；对方 MUST NOT 收到可见的提示，但其客户端会被告知关系解除，以便移除对方的动态。

#### Scenario: 查看好友列表
- **WHEN** `alice` 与 `bob`、`carol` 是好友，`alice` 查询好友列表
- **THEN** 返回 `bob` 与 `carol`，各带昵称、头像与在线状态

#### Scenario: 删除好友
- **WHEN** `alice` 删除好友 `bob`
- **THEN** 两人都不再出现在彼此的好友列表中，`bob` 的界面上没有任何提示，时间线中 `alice` 的动态消失

### Requirement: 拉黑

用户 SHALL 能拉黑任意账号并能解除。拉黑 MUST 同时解除双方的好友关系并清除双方之间的待处理申请。被拉黑者 MUST NOT 能再向拉黑者申请，MUST NOT 能看到拉黑者的主页与动态，MUST NOT 能在拉黑者的动态下点赞或评论。用户 SHALL 能查看自己的拉黑名单。

#### Scenario: 拉黑好友
- **WHEN** `alice` 拉黑好友 `bob`
- **THEN** 两人不再是好友，`bob` 查看 `alice` 的主页时得到 `user_not_found`

#### Scenario: 解除拉黑
- **WHEN** `alice` 解除对 `bob` 的拉黑
- **THEN** `bob` 可以再次向 `alice` 申请，但两人不会自动恢复好友关系

#### Scenario: 拉黑名单
- **WHEN** `alice` 拉黑了 `bob` 与 `dave` 后查看拉黑名单
- **THEN** 返回 `bob` 与 `dave`

### Requirement: 好友申请的频率限制

每个账号发送好友申请 MUST 受频率限制，默认每小时 30 次。超出时系统 MUST 返回 `rate_limited` 并带重试时间。

#### Scenario: 超出申请频率
- **WHEN** `alice` 在一小时内已发出 30 次好友申请，再发第 31 次
- **THEN** 系统返回 `rate_limited`，附带可重试的等待时间
