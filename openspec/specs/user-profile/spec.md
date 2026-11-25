# user-profile Specification

## Purpose
定义每个账号可被他人认识的身份资料（昵称、签名、头像），以及主页的查看、编辑与从主页发起私聊。

## Requirements

### Requirement: 主页资料字段

每个账号 SHALL 有昵称、个性签名与头像三项资料。昵称 1–24 个字符，未设置时显示为用户名；签名不超过 60 个字符、单行；头像是一个显示宽度不超过 2 的字符或 emoji 加一个颜色名，未设置时为用户名首字符加按用户名决定的默认颜色。用户名仍是唯一且不可改的标识。

#### Scenario: 新账号的默认资料
- **WHEN** `alice` 刚注册，尚未修改资料
- **THEN** 她的昵称显示为 `alice`，签名为空，头像为 `a` 与一个固定的默认颜色

#### Scenario: 同一账号的默认颜色稳定
- **WHEN** 任何人在任何时候查看 `alice` 的默认头像
- **THEN** 颜色都相同

### Requirement: 修改自己的资料

用户 SHALL 能修改自己的昵称、签名与头像，修改 MUST 立即对所有能看到其资料的人生效。不合规的值 MUST 被拒绝并返回 `invalid_profile`，原值不变。昵称与签名 MUST NOT 含换行或控制字符。

#### Scenario: 修改昵称
- **WHEN** `alice` 把昵称改为"小爱"
- **THEN** 她的好友再次查看她的主页或好友列表时看到"小爱"

#### Scenario: 签名过长
- **WHEN** `alice` 提交 61 个字符的签名
- **THEN** 系统返回 `invalid_profile`，签名保持原值

#### Scenario: 头像字符过宽
- **WHEN** `alice` 提交由三个汉字组成的头像字符
- **THEN** 系统返回 `invalid_profile`

#### Scenario: 不认识的颜色
- **WHEN** `alice` 提交颜色名 `ultraviolet`
- **THEN** 系统返回 `invalid_profile`

### Requirement: 资料变更的推送

资料修改后，系统 SHALL 向本人的其他在线连接及其在线好友推送资料更新事件，使他们界面上的昵称与头像随即更新。

#### Scenario: 好友看到新头像
- **WHEN** `alice` 在线的好友 `bob` 正在看会话列表，`alice` 修改头像
- **THEN** `bob` 收到资料更新事件，会话列表中 `alice` 的头像随即改变

### Requirement: 查看主页

已登录用户 SHALL 能按用户名查看任意未拉黑自己的账号的主页：昵称、用户名、签名、头像、注册时间、双方是否为好友，以及此人对查看者可见的最近动态（最多 20 条，可继续翻页）。被拉黑者查看时 MUST 得到 `user_not_found`。非好友查看时动态部分 MUST 为空。

#### Scenario: 查看好友主页
- **WHEN** `alice` 与 `bob` 是好友，`alice` 查看 `bob` 的主页
- **THEN** 返回 `bob` 的资料、"是好友"，以及 `bob` 设为所有好友可见的最近动态

#### Scenario: 查看陌生人主页
- **WHEN** `alice` 与 `carol` 不是好友，`alice` 查看 `carol` 的主页
- **THEN** 返回 `carol` 的资料与"不是好友"，动态为空

#### Scenario: 查看自己的主页
- **WHEN** `alice` 查看自己的主页
- **THEN** 返回她的全部动态，包括仅自己可见的

### Requirement: 从主页发私聊

在别人的主页上 SHALL 有发消息的操作，其效果 MUST 与按该用户名打开私聊相同。

#### Scenario: 一键发消息
- **WHEN** `alice` 在 `bob` 的主页上选择发消息
- **THEN** 打开 `alice` 与 `bob` 的私聊，焦点在输入框

### Requirement: 用户列表带资料

返回用户的接口（用户列表、好友列表、申请列表）MUST 为每个用户附带昵称与头像，使客户端无需逐个查询主页。

#### Scenario: 用户列表带昵称
- **WHEN** 客户端请求用户列表
- **THEN** 每个用户带有昵称、头像字符与头像颜色
