# Design

## Context

动机见 `proposal.md` 的 Why。与本设计直接相关的现状：

- `Storage` 接口（`pkg/storage/storage.go`）已按领域形状定义，36 个方法全部返回领域结构与三个哨兵错误（`ErrNotFound` / `ErrConflict` / `ErrUnavailable`）。`pkg/server` 只通过这个接口访问存储，并把哨兵错误映射为协议错误码。
- 当前实现是"单 actor 协程 + 全量内存镜像 + JSON 文件"：`model` 结构把全部用户、令牌、会话、成员、消息放在内存里，写操作在 actor 内先改内存、再整文件落盘、失败则回滚内存。
- 搜索语义是 `strings.Contains(strings.ToLower(body), strings.ToLower(keyword))`——大小写不敏感的子串匹配，不区分词边界，中文按字符匹配。
- 消息序号由 actor 内的 `nextSeq` map 分配，与追加写在同一 actor turn 内完成，因此无冲突、无空洞。
- `SearchCursor` 的形状（`Conv` 索引 + `Seq`）绑定了"按会话 ID 排序、组内从新到旧"的扫描顺序。

## Goals / Non-Goals

**Goals:**

- 用 SQLite 替换 JSON 文件，`Storage` 接口签名与领域结构一字不改，`pkg/server`/`pkg/app`/客户端零改动。
- 进程内存不再随历史消息增长：读路径直接查询数据库，只对每次请求需要的数据分配内存。
- 单条消息的持久化成本与同期历史长度无关（一行插入 vs 重写整个会话文件）。
- 搜索不再持有"存储 actor"这类全局串行点：读操作之间并发，写操作只在 SQLite 的写锁上排队。
- 保持现有的全部对外行为：序号连续、幂等、分页不重不漏、崩溃不损坏、错误统一映射。

**Non-Goals:**

- 目录与文件权限收紧（0700/0600）、静态加密、审计日志防篡改哈希链——用户明确将安全加固排除在本次范围外。
- 数据迁移：项目未上线，不写 JSON → SQLite 的导入工具，旧数据目录直接丢弃。
- 多机部署或共享数据库：仍是单进程、本地文件。
- 改变搜索的匹配语义（见决策 5）。

## Decisions

### 1. 用 `modernc.org/sqlite`，DSN 携带 pragma

**选择**：`modernc.org/sqlite v1.62.0`（纯 Go，`go build` 不需要 gcc），DSN 形如
`file:<dir>/linkchat.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate`。

**理由**：本机确实没有 gcc（已验证），`mattn/go-sqlite3` 不可用。DSN 里带 pragma 而不是打开后再逐条 `PRAGMA`，是因为 `database/sql` 的连接池会新建连接，`PRAGMA` 是连接级状态，只在池里第一条连接上执行的那些设置会静默丢失。

**实测确认**：`sqlite_version: 3.54.1`；`_pragma=journal_mode(WAL)` 与 `busy_timeout` 均在池内生效；WAL 下一个写事务打开期间，另一条连接的读不被阻塞。

**替代方案**：
- *`mattn/go-sqlite3`*：需要 cgo 与 gcc，本机不具备，且会让交叉编译变复杂。
- *打开后逐条 `PRAGMA`*：连接池下不可靠，容易在"测试通过、线上偶发"的地方踩坑。

**代价**：依赖树增加约 6 个模块（libc、memory、mathutil 等），二进制体积增长若干 MB；`modernc` 的纯 Go 实现比 cgo 版本在极端写入吞吐上略慢，对本项目规模无实质影响。

### 2. 写操作用"短事务 + `_txlock=immediate`"，不再保留 actor

**选择**：每次写操作开一个 `BEGIN IMMEDIATE` 事务，完成即提交；`database/sql` 连接池（`SetMaxOpenConns` 设上限）负责并发。读操作用普通连接直接查询。

**理由**：actor 的存在原本是为了给"内存镜像 + 文件"提供串行化与一致性；换成 SQLite 后，这两个职责由事务与行锁承担，再套一层 actor 只会把读也串行化，白白损失并发。`_txlock=immediate` 让事务一开始就拿写锁，避免"先读后写"在升级为写锁时遇到 `SQLITE_BUSY` 而需要重试。

**代价与边界**：并发写会在 SQLite 的写锁上排队，这是有意为之——SQLite 同一时刻只有一个写者。排队由 `busy_timeout(5000)` 兜底，超时返回 `ErrUnavailable`，与服务端既有的"存储不可用"映射路径一致。

**替代方案**：
- *保留 actor 包一层 `*sql.DB`*：读也被串行化，失去了换引擎的最大收益之一（搜索结果不再阻塞发消息）。
- *每操作独立连接、默认 deferred 事务*：在"先 SELECT 再 INSERT"的路径上更容易触发 `SQLITE_BUSY`。

### 3. 表结构与索引

```
users(id TEXT PK, username, username_key UNIQUE, password_hash, created_at,
      security_question, security_answer_hash, perms TEXT, super INTEGER)
tokens(hash TEXT PK, user_id, created_at, expires_at, purpose,
       remote, device, last_seen_at, client_version)
  INDEX tokens_user(user_id)
conversations(id TEXT PK, kind, name, topic, created_by, created_at, member_ids TEXT)
  UNIQUE INDEX conv_channel_name(name) WHERE kind='channel'
members(conversation_id, user_id, joined_at, PRIMARY KEY(conversation_id, user_id))
messages(id TEXT PK,
         conversation_id NOT NULL, seq NOT NULL,
         author_id, body, client_msg_id DEFAULT '', created_at, edited_at, deleted)
  UNIQUE INDEX messages_conv_seq(conversation_id, seq)
  UNIQUE INDEX messages_idem(conversation_id, author_id, client_msg_id) WHERE client_msg_id <> ''
  INDEX messages_id_lookup(id)            -- MessageByID
  INDEX messages_author_time(author_id, created_at DESC)
  INDEX messages_time(created_at DESC, id DESC)
conversation_seq(conversation_id TEXT PK, next_seq INTEGER NOT NULL DEFAULT 1)
user_settings(user_id, key, value, PRIMARY KEY(user_id, key))
sanctions(id TEXT PK, user_id, kind, reason, created_by, created_at,
          expires_at, revoked_by, revoked_at, superseded_by)
  INDEX sanctions_user(user_id, created_at)
audit(id INTEGER PK AUTOINCREMENT, at, actor_id, actor_name, action, target, detail TEXT)
  INDEX audit_at(at DESC)
  INDEX audit_actor(actor_id, at DESC)
```

**要点**：

- `perms`、`member_ids`、`detail` 用 JSON 编码存在 TEXT 列——它们是小数组/小 map，且从不参与查询条件；为它们建关联表只会增加连接开销而没有查询收益。
- 幂等由 `messages_idem` 这个**部分唯一索引**在数据库层保证，不再依赖"遍历会话内消息找相同 client_msg_id"。实测重复插入返回 `SQLITE_CONSTRAINT_UNIQUE`（code 2067），据此识别为 `ErrConflict` 并回查已有行返回。
- `messages_conv_seq` 唯一索引同时服务两类查询：`ORDER BY seq DESC LIMIT n` 的翻页，与"写入时确保 seq 不重复"的约束。

### 4. 序号分配：单条 UPSERT … RETURNING，与插入同事务

**选择**：

```sql
INSERT INTO conversation_seq(conversation_id, next_seq) VALUES (?, 2)
ON CONFLICT(conversation_id) DO UPDATE SET next_seq = next_seq + 1
RETURNING next_seq - 1
```

在同一条 `BEGIN IMMEDIATE` 事务里紧接着 `INSERT INTO messages`，一起提交。

**理由**：`nextSeq` 的内存 map 没有对应物——序号现在必须由数据库分配，且必须与消息行在同一事务里落盘，否则崩溃会留下"序号被消耗但消息不存在"的空洞，破坏 `message-delivery / 消息顺序保证` 与分页的连续性。

**实测确认**：200 个 goroutine 并发追加同一会话，得到 1..200 恰好一次、无重复、无空洞。

**替代方案**：
- *`SELECT MAX(seq)+1`*：并发下重复，且需要额外索引扫描。
- *用 `messages` 表自身的 `MAX(seq)` 加唯一索引冲突重试*：错误路径复杂，且每次发送多一次全索引反向扫描。

### 5. 搜索：LIKE 子串匹配 + trigram FTS5 索引，而不是 MATCH 查询

这一决策是本设计里与最初设想出入最大的一处，依据是实测而非推断。

**实测结果**（200,000 行，同一张表）：

| 查询方式 | 关键词 `发布` | 关键词 `needle` | 关键词 `v2 版本` |
|---|---|---|---|
| FTS5 `MATCH`（trigram tokenizer） | **无结果** | 命中 | **无结果** |
| `LIKE '%发布%'` 扫普通表 | 命中，约 20ms | 命中，约 20ms | 命中 |
| `LIKE '%发布%'` 扫 trigram 索引表 | 命中，约 35ms | 命中，0.5ms | 命中 |

原因：trigram 分词器按 3 个字符为一组建立索引，长度为 2 的查询词产生不出 trigram，因此 `MATCH` 对它永远无结果；而本项目的首要语言是中文，两字词（"发布"、"版本"、"上线"）是最常见的搜索输入。换成 `unicode61`/`porter` 分词器虽然能匹配两字词，但它们的语义是**按词匹配**而非子串匹配——`elea` 搜不到 `Release`，这直接违反了 `admin-console / 跨会话搜索` 要求的"关键词"行为（现有实现是子串）。

**选择**：

- 关键词仍是**大小写不敏感的子串匹配**（`LIKE`，ASCII 大小写由 SQLite 的 `LIKE` 默认语义处理，与现有 `strings.ToLower` + `Contains` 等价），保持行为不变。
- 用 `fts5(body, content='messages', content_rowid='id', tokenize='trigram')` 作为外部内容索引，查询写成 `body LIKE ?` 对准这张索引表。SQLite 会把 trigram 索引用于 `LIKE` 的最左最短 trigram 定位，长关键词（≥3 字符）直接受益；短关键词退化为扫描，但仍在索引列上而非回收行。
- 关键词中的 `%` 与 `_` 用 `ESCAPE` 子句转义，否则用户输入的百分号会被当成通配符（实测 `%inside%` 会因未转义而误命中，见验证记录）。
- 索引由**触发器**或应用写路径同步维护（`messages` 的 INSERT 与 `UPDATE` 都更新 FTS 行），不采用"定期重建"——搜索必须立刻看到刚发出的消息。

**代价**：短关键词（1–2 个字符）仍有约 20–35ms/20 万行的扫描成本，且这段时间内 `LIKE` 在索引表上串行。这已经好于现状（现状是"在存储 actor 内扫内存，期间所有发送排队"），但需要承认它不是毫秒级。

**替代方案**：
- *纯 `MATCH` + trigram*：中文两字词零结果，功能不可用。
- *纯 `MATCH` + unicode61*：变成按词匹配，`elea` 搜不到 `Release`，行为回退。
- *`LIKE` 不做索引*：20 万行 20ms 尚可接受，但 100 万行会到 100ms 量级且随行数线性增长；保留 trigram 索引几乎无额外成本（只在写入时多一行索引更新）。

### 6. 搜索结果排序改为全局按时间倒序，游标随之改变

**选择**：`SearchResult` 按 `created_at DESC, id DESC` 全局排序，`SearchCursor` 改为"上一批最后一条消息的 `(created_at, id)`"。批量取用 keyset 分页：

```sql
... AND (created_at < ? OR (created_at = ? AND id < ?))
ORDER BY created_at DESC, id DESC LIMIT ?
```

**理由**：`admin-console / 跨会话搜索` 明确要求"按时间从新到旧排列"，而现有实现是按会话 ID 分组、组内倒序，跨会话时并非全局时间序。管理后台前端只把游标原样回传（`pkg/server/adminweb/app.js` 用 `cursor_conv`/`cursor_seq` 透传），因此游标字段名改为 `cursor_at`/`cursor_id` 对页面无影响，但这是一个**存储内部 API 的破坏性变更**——已在 proposal 中标注。

**副作用**：`SearchMaxScan`（单次调用最多扫 2 万条的上限）不再需要。它存在的原因是"扫描会占用存储 actor"，而现在读不阻塞写；移除后一次搜索可以扫到自然结束或凑满一页。

**替代方案**：保留按会话分组的旧顺序——与管理后台规格冲突，且在 SQLite 下需要为每个会话单独查询再归并，实现更复杂。

### 7. 错误映射：把 SQLite 错误收敛为三个哨兵

**选择**：实现内部统一用 `errors.As(err, &*sqlite.Error)` 取出 `Code()`：

- `1555`（`SQLITE_CONSTRAINT_PRIMARYKEY`）/ `2067`（`SQLITE_CONSTRAINT_UNIQUE`）→ `ErrConflict`
- `sql.ErrNoRows` → `ErrNotFound`
- `SQLITE_BUSY` / `SQLITE_IOERR` / `SQLITE_READONLY` / `SQLITE_CANTOPEN` / `sql.ErrConnDone` / `sql.ErrTxDone` → `ErrUnavailable`
- 其余原样返回

**理由**：服务端只认识三个哨兵错误（`handlers.go` 里 `errors.Is(err, storage.ErrUnavailable)` 决定是否返回 `storage_unavailable`）。让 SQLite 的错误码与文本穿过接口会把数据库细节暴露到协议层，违反 `server-runtime / 存储错误被统一映射`。

**注意**：实测主键冲突返回 1555、复合唯一索引冲突返回 2067，两者都要映射，不能只判 `Errors.Is(err, sqlite.ErrConstraint)`——该封装在 modernc 里并不总是可用，直接用数字码更稳。

### 8. 打开入口与文件布局

**选择**：`storage.Open(dir) (Storage, error)`，数据库文件为 `<dir>/linkchat.db`（WAL 模式会附带 `-wal`、`-shm`）。`Open` 内执行建表（`CREATE TABLE IF NOT EXISTS`）与索引创建，不做版本化迁移——项目未上线，schema 变更直接改 `CREATE` 语句。

**理由**：没有已部署的数据，引入 migrate/schema_version 表是纯粹的过度设计。用户明确要求不考虑数据兼容。

**代价**：一旦上线，第一次 schema 变更就必须补上迁移机制。这一点写进 Risks。

### 9. 测试改造

**选择**：

- `newTestStore` 从 `OpenJSON(dir)` 改为 `Open(dir)`，其余测试**逐条保留**——它们是规格场景的代理，改的是构造方式而不是断言内容。
- 依赖内部结构的辅助函数重写：
  - `newJSONStoreForBench`/`(*jsonStore).bulkMessages` → `(*sqlStore).bulkMessages`，改为在单事务里插入 N 行（比逐行 `AppendMessage` 快，语义与"内存构造"等价）。
  - `admin_test.go` 里"写 `users.json` 造出一个旧账号"改为"INSERT 一行没有 `perms` 的 users 记录"。
  - `TestTornTrailingLineIsIgnored` 针对的是 JSON 追加的半行；SQLite 下由事务保证，该测试**删除**，其意图（崩溃不留下半截）改由一条"事务中途 Close 后重开，数据完整"的测试承接。
  - `TestWriteFileAtomicReplacesWholeFile` 随 `writeFileAtomic` 一起删除。
- 新增测试：并发追加序号（已有同类）、`LIKE` 转义（`%`/`_` 字面量）、FTS 索引在编辑/删除后同步、搜索游标跨批次不重不漏、`duplicate client_msg_id` 返回既有行。

## Risks / Trade-offs

- **短关键词搜索仍是全索引扫描** → 已量化（20 万行约 20–35ms）；不阻塞写入，且返回结果有 100 条上限。若将来成为瓶颈，再上外部分词或专门的搜索服务，属于可观测后的优化，不是当前的未知风险。
- **FTS 索引需要写路径维护，写漏就搜索不全** → 用数据库触发器维护 `messages_fts`，而不是应用层手写"记得更新"；并加一条测试断言"编辑或软删除后，FTS 行为与 messages 一致"。
- **SQLite 单写者限制** → 并发写在写锁排队。对本项目（单机、聊天量级）是合适的；`busy_timeout` + `ErrUnavailable` 提供了明确的失败路径，而不是无界等待。
- **没有 schema 迁移机制** → 上线后第一次改表需要补一版 `PRAGMA user_version` 驱动的迁移。现在引入是浪费，但要写进 README/设计备忘。
- **依赖体积** → `modernc.org/sqlite` 让二进制增大约数 MB。相对"不需要 gcc、跨平台 `go build` 即得"的收益，接受。
- **`SearchCursor` 变更** → 存储内部 API 破坏性变更，但唯一消费者（管理后台前端）把它当作不透明值透传，无需改动；服务端 `admin_api.go` 的查询参数名需同步改。
- **WAL 文件与 dirlock 的交互** → `pkg/dirlock` 锁的是数据目录下的 `server.lock`，与此无关；但服务端运行期间直接复制 `.db` 会得到不一致快照，运维文档需说明备份要用 `VACUUM INTO` 或停机复制。

## Migration Plan

项目未上线，无数据迁移。落地顺序见 `tasks.md`：

1. 加依赖、建 schema 与打开函数。
2. 按领域分块实现 `Storage`（用户/令牌 → 会话/成员 → 消息 → 设置 → 处分/审计 → 概览与搜索）。
3. 每块完成后跑该块对应的现有测试（测试先改为 `Open`）。
4. 全部通过后删除 JSON 实现与其专属测试，更新调用点、README 与设计文档。

**回滚策略**：本变更在单个分支上完成；若中途放弃，回滚分支即可——没有数据格式残留（旧数据目录本来就不需要保留）。

## Open Questions

- 是否把 `messages.body` 的 FTS 索引做成外部内容表（`content='messages'`）还是独立 `fts5(body)` 表。外部内容表省一份正文存储，但重建索引的路径更绕；独立表多占一份空间但读写路径直白。倾向于外部内容表，实施时以"删除/编辑后搜索正确"的测试为准。
- `ListUsers` 目前被健康检查当作"存储是否可用"的探针调用。换成 SQLite 后这个探针的开销从"遍历内存 map"变成一次全表扫描；可以改为 `SELECT 1`，但这会改动 `pkg/server`，需要在实施时确认是否属于本次范围（倾向于：留下现状，只把 `ListUsers` 实现得足够快；若实测有影响再单独提变更）。
