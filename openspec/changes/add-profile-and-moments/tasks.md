# Tasks

## 1. 协议

- [x] 1.1 在 `pkg/protocol` 新增好友、主页、动态的 op 与事件常量、请求/响应/事件载荷类型；`User` 加 `nickname/avatar/color`，`Message` 加 `author_avatar/author_color`；新增错误码 `already_friends`、`invalid_profile`、`moment_not_found`、`moment_too_long`、`not_found` 及其文案；验证：`go test ./pkg/protocol/...` 中新增的错误码唯一性与载荷往返测试通过
- [x] 1.2 在 `pkg/protocol` 新增头像校验与默认头像函数（单字形簇、显示宽度 ≤ 2、非空白非控制字符；颜色为六个命名色之一；默认为用户名首字符大写 + 按用户名哈希的颜色）；验证：单元测试覆盖合法 emoji、宽字符、三个汉字被拒、控制字符被拒、默认颜色稳定

## 2. 存储

- [x] 2.1 在 `pkg/storage` 新增表 `user_profiles`、`friendships`、`friend_requests`、`blocks` 及 `Storage` 方法（资料读写、好友增删查、申请增删查与合并、拉黑增删查、`AreFriends`、`IsBlocked`、`FriendIDs`）；验证：`pkg/storage` 新测试覆盖一对只存一行、重复申请合并、拉黑清除好友与申请
- [x] 2.2 新增表 `moments`、`moment_likes`、`moment_comments`、`moment_notifications`、`moment_seen` 及方法（发布、按作者集合分页取时间线、按作者分页、取单条、删除连带点赞评论通知、点赞幂等、评论增删、通知增查与全部已读、未读计数、seen 时刻读写、后台全量分页）；验证：测试覆盖 keyset 分页不重不漏、删除动态后点赞评论通知消失、重复点赞只一条

## 3. 服务端：好友与主页

- [x] 3.1 新增 `pkg/server/friends.go`：`friend.request`（合并、互相申请即成好友、对拉黑者返回 `user_not_found`、频率限制）、`friend.respond`、`friend.list`、`friend.remove`、`friend.block`、`friend.unblock`，以及 `friend.requested`、`friend.added`、`friend.removed` 推送；验证：`pkg/server` 新测试逐条覆盖 `friends` spec 的场景
- [x] 3.2 新增 `pkg/server/profile.go`：`profile.get`（拉黑者得 `user_not_found`、非好友动态为空、本人含仅自己可见）、`profile.update`（校验、禁言拒绝、推送 `profile.updated` 给本人其他连接与在线好友）；`user.list` 与消息载荷补全资料；验证：测试覆盖 `user-profile` spec 的场景与禁言改资料被拒

## 4. 服务端：动态

- [x] 4.1 新增 `pkg/server/visibility.go`：`canSeeMoment`、`filterInteractions`，以及把存储动态转成协议载荷的函数；验证：表驱动单元测试覆盖作者、好友、非好友、拉黑、仅自己可见、共同好友与非共同好友的评论和点赞、回复对象不可见时置空
- [x] 4.2 新增 `pkg/server/moments.go`：`moment.post`、`moment.timeline`（第一页更新 seen）、`moment.delete`、`moment.like`、`moment.comment`、`moment.comment.delete`、`moment.notifications`、`moment.notifications.read`、`moment.unread`；禁言与频率限制；生成通知；按接收者推送 `moment.new`、`moment.updated`、`moment.deleted`、`moment.notify`；在 `dispatch` 注册全部新 op；`Config` 增加三项频率阈值；验证：测试逐条覆盖 `moments` spec 的场景与 `account-moderation` 禁言场景

## 5. 管理后台

- [x] 5.1 新增后台接口 `GET /admin/api/moments`（`chat.view`）、`POST /admin/api/moments/{id}/delete` 与 `POST /admin/api/comments/{id}/delete`（`users.moderate`），删除后推送并写审计 `delete_moment`/`delete_comment`；验证：`pkg/server` 测试覆盖只读管理员被拒删除、删除写审计并含正文
- [x] 5.2 管理后台页面新增「动态」区域（作者筛选、分页、可见范围标记、评论展开、确认后删除），审计页加两个动作的中文名与详情；验证：在浏览器中打开 `/admin/#moments`，查看、筛选、删除各操作一遍

## 6. 客户端：状态与入口

- [x] 6.1 `pkg/client`：新增 `EntryMoments`（排在最前）、`PageMoments`、`PageProfile`；`AppState` 增加 `Profiles` 缓存与朋友圈未读计数；`pkg/i18n` 新增所有文案（中英）；验证：`pkg/client` 测试更新入口顺序与页面映射，`pkg/tui/views` 侧边栏 i18n 测试覆盖新入口
- [x] 6.2 `pkg/app/moments.go`：登录后拉取 `moment.unread`、`user.list` 填充资料缓存；处理全部新事件（新动态、更新、删除、通知、好友申请/添加/删除、资料更新）；朋友圈与主页的全部网络调用；Alt+P；连旧服务端时标记不支持；验证：`pkg/app` 测试用假传输覆盖事件更新状态与未读计数

## 7. 客户端：界面

- [x] 7.1 `views.MomentsPage`：标签栏、时间线（发动态行、动态块、点赞与评论、仅自己可见标记、选中态、滚动）、通知（申请与互动通知）、好友（添加好友行、好友、拉黑名单）、输入框（发动态带可见范围、评论、回复、加好友）、确认提示；验证：`pkg/tui/views` 渲染测试为每个标签断言关键文本与选中行
- [x] 7.2 `views.ProfilePage`：大号头像、昵称、`@用户名`、签名、注册日期、好友状态、动态列表、发消息/加好友/编辑资料操作与编辑表单（颜色循环、即时预览）；验证：渲染测试覆盖他人主页与自己主页、编辑态
- [x] 7.3 `pkg/app` 按键：朋友圈页面的标签切换、上下选择、`l c r d a x m b u p` 与 Enter/Esc，主页的 `m`、加好友、编辑与保存，滚到底自动加载下一页；入口未读标记渲染；验证：`pkg/app` 按键测试覆盖每个快捷键的效果
- [x] 7.4 头像渲染：会话列表私聊行与消息作者名前画头像，单色终端只画字符；验证：`pkg/tui/views` 测试断言私聊行首两格为头像且宽 1 的字符补空格

## 8. 端到端与收尾

- [x] 8.1 端到端测试（真实服务端 + 两个客户端 App）：加好友、发动态、对方时间线出现、点赞产生通知与入口计数、评论可见性、删除好友后动态消失；验证：`go test ./pkg/app/ -run Moments` 通过
- [x] 8.2 文档：`docs/client.md` 新增朋友圈与主页使用说明与快捷键；`docs/server.md` 新增频率限制配置项与后台「动态」区域；帮助页加入新快捷键；验证：文档中列出的快捷键与 `keys.go` 实现一一对应
- [x] 8.3 根目录 `VERSION` 改为 `4.0.0`，验证 `go test ./pkg/version/...` 通过
- [x] 8.4 `CHANGELOG.md` 新增 4.0.0 一节；验证：内容覆盖好友、主页、朋友圈、后台动态、禁言范围五部分
- [x] 8.5 全量 `go vet ./...`、`gofmt -l`、`go test ./...` 通过，并实际启动服务端与两个客户端手测一遍完整流程

## Workflow follow-up

- 实现完成后归档本变更，同步 `friends`、`user-profile`、`moments` 新主 spec 与 `tui-chat-client`、`chat-protocol`、`admin-console`、`account-moderation` 的修改。
