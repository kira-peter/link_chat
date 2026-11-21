# Tasks

## 1. 依赖与骨架

- [x] 1.1 在 `go.mod` 加入 `modernc.org/sqlite`，运行 `go build ./...` 验证纯 Go 编译通过（无需 gcc）
- [x] 1.2 新增 `pkg/storage/sqlite.go`：`Open(dir) (Storage, error)` 打开 `<dir>/linkchat.db`，DSN 携带 `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate`；执行全部 `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`（表结构见 design.md 决策 3），验证：新建空目录调用 `Open` 后文件存在且可重复打开不报错
- [x] 1.3 新增 `sqlStore` 类型持有 `*sql.DB`，实现 `Close() error`（`db.Close()`）；新增统一错误映射函数（SQLite 错误码 1555/2067 → `ErrConflict`，`sql.ErrNoRows` → `ErrNotFound`，busy/ioerr/readonly/cantopen/连接已关闭 → `ErrUnavailable`），验证：对映射函数的单元测试覆盖这三类输入

## 2. 用户与令牌

- [x] 2.1 实现 `CreateUser`/`UserByUsernameKey`/`UserByID`/`ListUsers`/`UpdateUserPasswordHash`/`UpdateUserCredentials`/`SetUserPerms`/`SetUserSuper`，`perms` 以 JSON 编码存入 TEXT 列；验证：`TestCreateUserRejectsDuplicateUsernameCaseInsensitively`、`TestUserLookupIsCaseInsensitive`、`TestUserPermsPersist`（改为直接 INSERT 构造旧账号而非写 `users.json`）通过
- [x] 2.2 实现 `SaveToken`/`TokenByHash`/`DeleteToken`/`DeleteTokensForUser`/`DeleteSessionTokensForUser`/`DeleteTokensForUserPurpose`/`ListTokensForUser`；验证：`TestTokenPurposes` 通过

## 3. 会话与成员

- [x] 3.1 实现 `CreateConversation`/`ConversationByID`/`ChannelByNameKey`/`ListConversations`/`DeleteConversation`/`DMBetween`，频道名唯一性用部分唯一索引 `WHERE kind='channel'`；验证：`TestDMBetweenIsOrderIndependent`、`TestDeleteConversationRemovesMessages` 通过
- [x] 3.2 实现 `AddMember`/`RemoveMember`/`IsMember`/`ListMembers`/`MemberCount`（DM 的成员仍来自 `conversations.member_ids`，channel 查 `members` 表）；验证：现有 `pkg/server` 相关测试（`server_test.go` 中 `MemberCount` 断言）在改用新存储后通过

## 4. 消息

- [x] 4.1 实现 `AppendMessage`：单个 `BEGIN IMMEDIATE` 事务内先 `INSERT ... ON CONFLICT DO UPDATE ... RETURNING` 分配 `conversation_seq.next_seq`，再插入消息行并提交；`client_msg_id` 冲突时捕获唯一约束错误并查询既有行返回；验证：`TestAppendMessageAssignsGapfreeUniqueSeq`（50 并发）与一个新增的 200 并发用例都得到 gap-free 唯一序号，`TestAppendMessageIsIdempotentByClientMsgID` 通过
- [x] 4.2 实现 `MessageByID`（走 `messages.id` 主键）、`MessagesBefore`（按 `conversation_id, seq` 索引翻页）、`UpdateMessage`（按 `id` 更新一行，不再整表重写）；验证：`TestMessagesBeforePagesBackwards` 通过
- [x] 4.3 新增 FTS5 trigram 索引表 `messages_fts`（`content='messages'`），用触发器在 `messages` 的 INSERT/UPDATE 后同步；验证：新增测试断言编辑和软删除后，`body LIKE` 命中结果与 `messages` 表当前内容一致（不含已被新正文覆盖的旧词）

## 5. 设置、处分、审计

- [x] 5.1 实现 `UserSettings`/`MergeUserSettings`（`user_settings` 表按 `user_id,key` 主键 upsert）；验证：`pkg/storage/settings_test.go` 通过
- [x] 5.2 实现 `AddSanction`/`RevokeSanction`/`SanctionsForUser`/`ActiveSanction`（supersede 逻辑在同一事务内完成：插入新制裁的同时把同用户同 kind 仍生效的旧记录标记 `superseded_by`）；验证：`TestSanctions` 通过
- [x] 5.3 实现 `AppendAudit`/`ListAudit`（按 `at DESC` 索引分页，过滤条件对应索引列做 WHERE，不再有"内存尾部 vs 磁盘"的区分，删除 `auditKeep` 相关逻辑）；验证：`TestAudit` 的全部断言在去掉"内存/磁盘分界"假设后仍能表达相同场景并通过（深分页场景改为验证分页本身，不再验证内存截断行为）

## 6. 概览与跨会话搜索

- [x] 6.1 实现 `Counts`（`SELECT count(*)` 分别查 users/conversations 按 kind/messages）；验证：`TestCounts` 通过
- [x] 6.2 实现 `SearchMessages`：关键词走 `messages_fts` 的 `body LIKE ? ESCAPE '\'`（对用户输入中的 `%`/`_` 转义），结合作者/会话/时间条件；结果按 `created_at DESC, id DESC` 全局排序；`SearchCursor` 改为 `{At int64, ID string}` 并用 keyset 分页（`(created_at, id) < (cursor.At, cursor.ID)`）；移除 `SearchMaxScan` 常量及相关逻辑；验证：`TestSearchMessages` 按新排序重写断言后通过，新增测试覆盖 `%`/`_` 字面量关键词、2万行以上的分页不重不漏
- [x] 6.3 更新 `pkg/server/admin_api.go` 的查询参数名（`cursor_conv`/`cursor_seq` → `cursor_at`/`cursor_id`）与 `pkg/server/adminweb/app.js` 对应的透传字段；验证：管理后台搜索分页手测通过（继续搜索按钮取到不重复的下一批）

## 7. 调用点切换与收尾

- [x] 7.1 `cmd/link-chat-server/main.go` 的 `storage.OpenJSON` 全部改为 `storage.Open`；验证：`go build ./...` 通过
- [x] 7.2 删除 `pkg/storage/json.go`、`json_impl.go`、`atomic.go`、`admin_helpers_test.go` 中的 `(*jsonStore)` 专属辅助，改为 `(*sqlStore)` 等价实现（批量插入走单事务多行插入）；删除 `TestWriteFileAtomicReplacesWholeFile`、`TestTornTrailingLineIsIgnored`（JSON 文件专属场景不再适用）；验证：`go vet ./...` 无未使用引用
- [x] 7.3 全量运行 `go test ./...`，确认 `pkg/storage`、`pkg/server`、`pkg/app` 全部通过；运行 `go test ./pkg/storage/... -bench=. -benchtime=1x` 确认基准测试（已改写为对新实现取数）可执行
- [x] 7.4 更新 `README.md` 的架构小节与 `pkg/storage` 一行描述（"单写者协程 + 原子提交"改为"SQLite + WAL"），更新根 `VERSION` 文件为 `3.0.0`，验证 `go test ./pkg/version/...` 通过

## Workflow follow-up

- 本变更按仓库规则不改变任何 spec（`skip_specs: true`），无需同步主 spec。
- 实现完成并通过全部验证后，运行 archive 工作流归档本变更。
