# Spec Delta

## Purpose

定义服务端管理后台：谁能进入、以什么身份进入、按什么权限看到哪些区域，以及概览、聊天浏览与搜索、用户、账号、审计各区域对外可见的行为。它让服务器运营者在浏览器里看清服务器状态和全部对话，并把管理能力按权限分给不同的人。

## ADDED Requirements

### Requirement: 后台入口

服务端 SHALL 在与聊天相同的监听地址上通过 `/admin/` 提供管理后台页面，页面随服务端一起发布，无需额外部署。配置关闭后台时，`/admin/` 下的所有路径 MUST 返回 404。

#### Scenario: 打开后台页面
- **WHEN** 后台处于开启状态，浏览器访问 `/admin/`
- **THEN** 服务端返回后台页面，未登录时显示登录表单

#### Scenario: 后台被关闭
- **WHEN** 配置关闭了后台，浏览器访问 `/admin/` 或任何 `/admin/api/` 接口
- **THEN** 服务端返回 404，聊天功能不受影响

### Requirement: 管理权限位

系统 SHALL 定义四个管理权限：`stats.view`（查看统计）、`chat.view`（查看聊天）、`users.moderate`（处置用户）、`accounts.manage`（管理账号）。账号 MAY 持有其中任意组合。没有记录权限的已有账号 MUST 视为不持有任何权限。

#### Scenario: 旧账号没有权限
- **WHEN** 升级前已注册的账号首次在新版本下被读取
- **THEN** 该账号不持有任何管理权限，聊天功能照常

#### Scenario: 权限组合
- **WHEN** 一个账号只被授予 `stats.view` 和 `chat.view`
- **THEN** 该账号可以查看概览和聊天，但不能处置用户或管理账号

### Requirement: 超级管理员

超级管理员 SHALL 持有全部四个权限。超级管理员只能由服务端命令行产生，后台中 MUST NOT 有任何途径创建超级管理员、降低其权限、封禁、禁言、重置其密码或强制其下线。

#### Scenario: 超级管理员拥有全部区域
- **WHEN** 超级管理员登录后台
- **THEN** 概览、用户、会话、搜索、审计、账号区域全部可见

#### Scenario: 不能处置超级管理员
- **WHEN** 持有 `users.moderate` 的管理员对超级管理员提交封禁、禁言、重置密码或强制下线
- **THEN** 系统返回 `forbidden`，不产生处分记录

#### Scenario: 不能修改超级管理员的权限
- **WHEN** 持有 `accounts.manage` 的管理员提交修改超级管理员的权限
- **THEN** 系统返回 `forbidden`，权限保持不变

### Requirement: 后台登录

后台 SHALL 使用聊天账号的用户名和密码登录，并沿用聊天登录的失败限流。只有至少持有一个管理权限、且未被封禁的账号能登录成功。登录成功后签发后台专用令牌，有效期 12 小时，令牌 MUST 只保存在浏览器无法被页面脚本读取的 cookie 中。

#### Scenario: 管理员登录成功
- **WHEN** 持有 `stats.view` 的账号用正确的用户名和密码登录后台
- **THEN** 系统签发后台令牌，页面进入后台主界面

#### Scenario: 普通用户无法登录后台
- **WHEN** 不持有任何管理权限的账号用正确的用户名和密码登录后台
- **THEN** 系统返回 `forbidden`，不签发令牌

#### Scenario: 凭据错误
- **WHEN** 提交错误的密码
- **THEN** 系统返回 `invalid_credentials`，并计入与聊天登录相同的失败限流

#### Scenario: 后台令牌过期
- **WHEN** 后台令牌签发超过 12 小时后再发起请求
- **THEN** 系统返回 `unauthorized`，页面回到登录表单

#### Scenario: 退出后台
- **WHEN** 管理员在后台点击退出
- **THEN** 该后台令牌被作废，之后使用它的请求返回 `unauthorized`

### Requirement: 后台会话与聊天会话相互独立

后台令牌与聊天会话令牌 MUST 互不通用。登录或退出后台 MUST NOT 影响同一账号的聊天会话，反之亦然。

#### Scenario: 登录后台不顶掉聊天
- **WHEN** 账号 `alice` 已在聊天客户端在线，又登录了后台
- **THEN** 聊天连接保持在线，不收到 `session.replaced`

#### Scenario: 后台令牌不能恢复聊天会话
- **WHEN** 有人在聊天连接上用后台令牌发起恢复会话请求
- **THEN** 系统返回 `unauthorized`

#### Scenario: 聊天令牌不能访问后台
- **WHEN** 有人用聊天会话令牌访问后台接口
- **THEN** 系统返回 `unauthorized`

### Requirement: 按请求实时校验权限

后台的每个请求 MUST 按账号当前的权限和封禁状态鉴权，权限变更和封禁 MUST 对下一个请求立即生效，无需重新登录。

#### Scenario: 收回权限立即生效
- **WHEN** 管理员 `bob` 已登录后台，其 `chat.view` 权限被收回，随后他请求某个会话的消息
- **THEN** 系统返回 `forbidden`

#### Scenario: 被收回全部权限
- **WHEN** 已登录后台的账号的全部管理权限被收回
- **THEN** 其后续后台请求返回 `forbidden`，页面提示无权访问并回到登录表单

### Requirement: 写操作的跨站防护

后台所有会改变状态的请求 MUST 拒绝来自其他站点页面的伪造提交。

#### Scenario: 跨站伪造被拒绝
- **WHEN** 管理员浏览器中的第三方页面向后台的封禁接口提交表单
- **THEN** 系统拒绝该请求，不产生任何处分记录

### Requirement: 区域按权限可见

后台 SHALL 只显示当前账号持有权限对应的区域，并且 MUST 在服务端对每个接口按同样的规则鉴权，而不是只隐藏界面入口。区域与权限的对应关系是：概览需要 `stats.view`；会话和搜索需要 `chat.view`；用户需要 `users.moderate`；账号和审计需要 `accounts.manage`。

#### Scenario: 只能看概览的管理员
- **WHEN** 只持有 `stats.view` 的管理员登录后台
- **THEN** 导航中只有「概览」，没有用户、会话、搜索、审计、账号

#### Scenario: 直接调用无权接口
- **WHEN** 只持有 `stats.view` 的管理员直接请求会话消息接口
- **THEN** 系统返回 `forbidden`

### Requirement: 概览

持有 `stats.view` 的管理员 SHALL 能看到服务器概览：运行时长、当前连接数、在线用户列表、用户总数、频道数、私聊数、消息总数、近 24 小时消息数、进程内存与协程数，以及最近的服务端日志（最多 500 条）。在线人数与连接数 MUST 实时更新。

#### Scenario: 查看概览
- **WHEN** 持有 `stats.view` 的管理员打开概览
- **THEN** 页面显示上述各项数值与最近日志

#### Scenario: 在线人数实时变化
- **WHEN** 概览页打开期间有用户上线或下线
- **THEN** 在线人数与在线用户列表在不刷新页面的情况下更新

### Requirement: 会话浏览

持有 `chat.view` 的管理员 SHALL 能列出全部频道和私聊（包括不含本人的私聊），并按会话从最新往前分页查看消息，包括作者、时间、正文、编辑与删除标记。已删除的消息只显示删除标记。会话打开期间，新消息、编辑和删除 MUST 实时反映。

#### Scenario: 查看他人私聊
- **WHEN** 持有 `chat.view` 的管理员打开 `alice` 与 `bob` 之间的私聊
- **THEN** 页面显示两人的消息记录，且该次查看被写入审计日志

#### Scenario: 向前翻页
- **WHEN** 管理员在会话中滚动到最早一条已加载的消息
- **THEN** 页面加载更早的一页消息，直到会话开头

#### Scenario: 实时追加
- **WHEN** 管理员正在查看频道 `general`，有人在其中发了一条消息
- **THEN** 该消息在不刷新页面的情况下出现在末尾

### Requirement: 跨会话搜索

持有 `chat.view` 的管理员 SHALL 能按关键词、作者、会话、时间范围的任意组合跨会话搜索消息。每次返回 MUST 不超过 100 条，按时间从新到旧排列；结果未穷尽时 MUST 提供继续搜索的游标。每次搜索 MUST 写入审计日志，包括搜索条件。

#### Scenario: 关键词搜索
- **WHEN** 管理员搜索关键词「发布」
- **THEN** 返回所有会话中正文包含「发布」且未被删除的消息，每条带所属会话与作者

#### Scenario: 组合条件
- **WHEN** 管理员按作者 `alice` 和最近 7 天搜索
- **THEN** 只返回 `alice` 在最近 7 天内发出的消息

#### Scenario: 结果分批
- **WHEN** 符合条件的消息超过 100 条
- **THEN** 第一次返回 100 条和一个游标，用游标继续搜索返回后续结果且不重复

### Requirement: 用户列表与详情

持有 `users.moderate` 的管理员 SHALL 能查看全部用户列表（用户名、注册时间、是否在线、当前生效的处分、是否管理员），并打开用户详情查看其当前会话来源（地址、设备、签发时间）与完整处分历史。

#### Scenario: 查看用户详情
- **WHEN** 管理员打开用户 `carol` 的详情
- **THEN** 页面显示其会话来源与全部处分记录，包括已到期和已撤销的

#### Scenario: 列表显示处分状态
- **WHEN** 用户 `dave` 正处于禁言中
- **THEN** 用户列表中 `dave` 一行显示「禁言」及到期时间

### Requirement: 建号

持有 `accounts.manage` 的管理员 SHALL 能用用户名、初始密码和初始权限创建账号。用户名与密码 MUST 满足注册时相同的规则。新账号没有安全问题。

#### Scenario: 创建一个运维账号
- **WHEN** 管理员创建账号 `ops`，初始权限为 `stats.view`
- **THEN** `ops` 可以登录聊天客户端，也可以登录后台并只看到概览

#### Scenario: 用户名已存在
- **WHEN** 管理员用已被占用的用户名建号
- **THEN** 系统返回 `username_taken`，不创建账号

### Requirement: 分配权限

持有 `accounts.manage` 的管理员 SHALL 能授予或收回其他账号的管理权限，约束如下：
- 不能修改自己的权限。
- 只能授予自己持有的权限。
- 只有超级管理员能授予或收回 `accounts.manage`。

#### Scenario: 不能改自己的权限
- **WHEN** 管理员提交修改自己的权限
- **THEN** 系统返回 `forbidden`

#### Scenario: 不能授予自己没有的权限
- **WHEN** 持有 `accounts.manage` 和 `stats.view`、但不持有 `chat.view` 的管理员给他人授予 `chat.view`
- **THEN** 系统返回 `forbidden`，权限保持不变

#### Scenario: 非超级管理员授予管理账号权限
- **WHEN** 非超级管理员给他人授予 `accounts.manage`
- **THEN** 系统返回 `forbidden`

### Requirement: 审计日志查看

持有 `accounts.manage` 的管理员 SHALL 能按时间从新到旧分页查看审计日志，并可按操作人、操作类型和时间范围过滤。

#### Scenario: 按操作人过滤
- **WHEN** 管理员按操作人 `bob` 过滤审计日志
- **THEN** 只显示 `bob` 执行的管理操作

### Requirement: 实时事件流

后台 SHALL 提供实时事件流，推送用户上线下线、注册、登录、新消息、消息编辑与删除、处分变化。推送内容 MUST 按接收者当前的权限过滤：没有 `chat.view` 的接收者 MUST NOT 收到消息正文；权限被收回或令牌失效后，事件流 MUST 被关闭。服务端关停时事件流 MUST 随之结束。

#### Scenario: 只看统计的管理员不收消息正文
- **WHEN** 只持有 `stats.view` 的管理员打开概览，有人发了一条消息
- **THEN** 该管理员的事件流可以更新消息计数，但不包含消息正文和所属会话

#### Scenario: 收回权限后事件流关闭
- **WHEN** 正在接收事件流的管理员的全部管理权限被收回
- **THEN** 其事件流被关闭，页面提示无权访问
