# moments Specification

## Purpose
定义朋友圈：好友之间分享纯文本动态，按可见范围展示在时间线上，并以点赞、评论互动，互动只在共同好友之间可见。

## Requirements

### Requirement: 发布动态

已登录用户 SHALL 能发布一条纯文本动态，正文去掉首尾空白后 1–500 个字符，可含换行。发布时 MUST 选择可见范围：所有好友，或仅自己。未指定时为所有好友。

#### Scenario: 发布成功
- **WHEN** `alice` 发布"今天天气不错"，可见范围为所有好友
- **THEN** 动态被保存，返回带标识、作者、正文、可见范围与发布时间的动态

#### Scenario: 正文为空
- **WHEN** `alice` 发布只含空格的动态
- **THEN** 系统返回 `invalid_argument`，不保存任何动态

#### Scenario: 正文过长
- **WHEN** `alice` 发布 501 个字符的动态
- **THEN** 系统返回 `moment_too_long`

### Requirement: 动态的可见性

一条动态 MUST 只对以下人可见：作者本人；可见范围为所有好友时，作者当前的好友。好友关系解除或拉黑后，对方 MUST 立即看不到这些动态，包括此前已看到的。对不可见的动态的任何操作 MUST 返回 `moment_not_found`。

#### Scenario: 好友可见
- **WHEN** `alice` 与 `bob` 是好友，`alice` 发布所有好友可见的动态
- **THEN** `bob` 的时间线中出现这条动态

#### Scenario: 仅自己可见
- **WHEN** `alice` 发布仅自己可见的动态
- **THEN** 只有 `alice` 自己的时间线与主页上有这条动态

#### Scenario: 删除好友后不可见
- **WHEN** `alice` 删除好友 `bob` 后，`bob` 刷新时间线
- **THEN** `alice` 的动态都不再出现

#### Scenario: 操作不可见的动态
- **WHEN** `carol` 不是 `alice` 的好友，却对 `alice` 的动态点赞
- **THEN** 系统返回 `moment_not_found`

### Requirement: 时间线

用户 SHALL 能获取自己的时间线：自己的全部动态与好友对其可见的动态，按发布时间从新到旧，每页最多 20 条。结果未完时 MUST 返回继续的游标，翻页 MUST 不重不漏。每条动态附带作者资料、对查看者可见的点赞与评论、查看者是否已点赞。

#### Scenario: 时间线排序
- **WHEN** `bob` 先发、`alice` 后发，二人互为好友，`alice` 获取时间线
- **THEN** `alice` 的动态在前，`bob` 的在后

#### Scenario: 翻页
- **WHEN** 时间线有 45 条动态，用户连续翻页直到没有游标
- **THEN** 共得到 45 条，没有重复

### Requirement: 点赞

能看到动态的用户 SHALL 能点赞与取消点赞，同一人对同一动态至多一个赞。重复点赞或取消未点过的赞 MUST 成功且不改变状态。

#### Scenario: 点赞
- **WHEN** `bob` 给 `alice` 的动态点赞
- **THEN** 该动态的点赞中出现 `bob`，`bob` 看到自己已点赞

#### Scenario: 重复点赞
- **WHEN** `bob` 对同一动态点赞两次
- **THEN** 点赞中只有一个 `bob`

#### Scenario: 取消点赞
- **WHEN** `bob` 取消对该动态的赞
- **THEN** 点赞中不再有 `bob`

### Requirement: 评论与回复

能看到动态的用户 SHALL 能发表评论，正文 1–200 个字符、单行。评论 MAY 指定回复同一动态下的另一条评论，此时显示为"回复 某人"。被回复的评论不存在或属于其他动态时 MUST 返回 `invalid_argument`。

#### Scenario: 评论
- **WHEN** `bob` 在 `alice` 的动态下评论"好看"
- **THEN** 该动态的评论中出现 `bob` 的"好看"

#### Scenario: 回复评论
- **WHEN** `alice` 回复 `bob` 的评论"谢谢"
- **THEN** 新评论显示为 `alice` 回复 `bob`："谢谢"

### Requirement: 互动只在共同好友之间可见

一条动态的点赞与评论 MUST 只对以下查看者显示：动态作者看到全部；其他查看者只看到自己的，以及与查看者是好友的人留下的。被回复者对查看者不可见时，回复对象 MUST 不显示具体名字。

#### Scenario: 共同好友可见
- **WHEN** `bob` 与 `carol` 都是 `alice` 的好友且互为好友，`carol` 评论 `alice` 的动态
- **THEN** `bob` 看到 `carol` 的评论

#### Scenario: 非共同好友不可见
- **WHEN** `bob` 与 `dave` 都是 `alice` 的好友但互不是好友，`dave` 评论 `alice` 的动态
- **THEN** `bob` 看不到 `dave` 的评论，`alice` 看得到

#### Scenario: 点赞同理
- **WHEN** `dave` 给 `alice` 的动态点赞
- **THEN** `alice` 看到 `dave` 的赞，`bob` 看不到

### Requirement: 删除动态与评论

作者 SHALL 能删除自己的动态，删除后其点赞与评论一并消失。评论者 SHALL 能删除自己的评论；动态作者 SHALL 能删除自己动态下的任何评论。删除他人的动态或无权的评论 MUST 返回 `forbidden`。

#### Scenario: 删除自己的动态
- **WHEN** `alice` 删除自己的一条动态
- **THEN** 这条动态从所有人的时间线和主页上消失

#### Scenario: 作者删除他人评论
- **WHEN** `alice` 删除 `bob` 在她动态下的评论
- **THEN** 该评论消失

#### Scenario: 删除别人的动态
- **WHEN** `bob` 试图删除 `alice` 的动态
- **THEN** 系统返回 `forbidden`

### Requirement: 新动态与互动的实时推送

有人发布、删除动态，或动态的点赞、评论发生变化时，系统 SHALL 向此后能看到该变化的在线用户推送事件，使其时间线即时更新；推送内容 MUST 遵守动态可见性与互动可见性的规则。

#### Scenario: 好友发了新动态
- **WHEN** `bob` 在线，好友 `alice` 发布所有好友可见的动态
- **THEN** `bob` 收到新动态事件

#### Scenario: 仅自己可见不推送给好友
- **WHEN** `alice` 发布仅自己可见的动态
- **THEN** `bob` 不收到任何事件

### Requirement: 互动通知

有人赞了或评论了我的动态、回复了我的评论时，系统 SHALL 为我生成一条通知，含谁、做了什么、哪条动态、评论内容、时间。自己对自己的操作 MUST NOT 产生通知。用户 SHALL 能获取通知列表（新到旧）、未读数，并把全部标为已读。在线时新通知 MUST 实时推送。

#### Scenario: 被点赞
- **WHEN** `bob` 给 `alice` 的动态点赞
- **THEN** `alice` 得到一条"`bob` 赞了你的动态"的未读通知

#### Scenario: 被回复
- **WHEN** `carol` 回复 `bob` 在 `alice` 动态下的评论
- **THEN** `alice` 得到一条评论通知，`bob` 得到一条回复通知

#### Scenario: 失去查看权限后隐藏通知

- **WHEN** 接收者当前无法查看原动态或该条互动
- **THEN** 通知不出现在列表、未读计数或实时推送中

#### Scenario: 标为已读
- **WHEN** `alice` 把通知全部标为已读
- **THEN** 她的未读数变为 0

### Requirement: 朋友圈未读

系统 SHALL 为每个用户记录最后一次查看时间线的时刻，并能报告此后好友发布、对其可见的新动态数。用户查看时间线第一页时 MUST 把该时刻更新为现在。

#### Scenario: 有新动态
- **WHEN** `alice` 上次看完时间线后，好友 `bob` 发布了两条动态
- **THEN** `alice` 的朋友圈未读数为 2

#### Scenario: 看过即清零
- **WHEN** `alice` 获取时间线第一页
- **THEN** 她的朋友圈未读数变为 0

### Requirement: 动态与评论的频率限制

每个账号发布动态默认每小时 20 条，评论与点赞合计默认每分钟 30 次，超出时 MUST 返回 `rate_limited` 并带重试时间。

#### Scenario: 发动态过快
- **WHEN** `alice` 一小时内发了 20 条动态后再发一条
- **THEN** 系统返回 `rate_limited`
