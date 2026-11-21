# Proposal

## Why

`pkg/storage` 当前的 JSON 文件实现把全部消息常驻内存、`MessageByID` 线性扫描全部会话、搜索在存储 actor 内扫全表、元数据每次改动整文件重写并 fsync。这些成本都随数据量线性恶化：内存占用与启动时间随历史消息增长，单条发送的延迟取决于一次磁盘 fsync，搜索期间所有发消息请求排队。`Storage` 接口从第一版起就按领域形状设计、明确预留了"换 SQLite 只需新增一个实现"的演进路径，现在补上这个实现。

项目尚未上线，没有线上数据，因此不做兼容层：直接替换实现，不保留旧数据迁移脚本。

## What Changes

- 新增 SQLite 实现（`modernc.org/sqlite`，纯 Go，不需要 cgo），作为 `Storage` 接口的唯一实现；删除 JSON 文件实现（`json.go`、`json_impl.go`、`atomic.go` 中不再被使用部分）。
- 存储形态从"多个 JSON 文件 + 全量内存镜像"变为单个数据库文件 + WAL：写入不再重写整文件，启动不再全量载入历史消息。
- 用索引取代线性扫描：按消息 ID 查、按会话与序号翻页、按作者与时间筛选都走索引。
- 用 FTS5 全文索引承载跨会话关键词搜索，替代"在存储 actor 内扫最多 2 万条"。搜索结果改为全局按时间从新到旧排序（当前实现是按会话 ID 分组、组内倒序，与管理后台规格要求的"按时间从新到旧"不符）。**BREAKING**（仅对存储内部 API）：`SearchCursor` 的语义随新排序改变，不再依赖固定会话顺序。
- 保留 `Storage` 接口签名、`Account`/`Message`/`Token` 等领域结构与服务的全部对外行为；`pkg/server`、`pkg/app`、客户端零改动。
- 打开入口从 `storage.OpenJSON(dir)` 改为 `storage.Open(dir)`，调用点（`cmd/link-chat-server`）相应修改。
- 现有测试从"通过 JSON 文件构造边界场景"改为"通过数据库构造"，测试意图与被验证的规格场景不变。
- 明确不在本次范围：目录与文件权限收紧、静态加密（SQLCipher 需要 cgo，与纯 Go 目标冲突）、审计日志防篡改哈希链。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

（无。本次只替换持久化实现，`server-runtime` 的"数据持久化语义"与"存储抽象"两条要求的对外行为不变——原子提交、崩溃不损坏、串行化写入、错误统一映射都仍然成立，只是由事务与 WAL 而不是临时文件加重命名提供。所有 spec 均不约束数据文件格式。）

## Version

Major：2.1.0 → 3.0.0。

本变更不新增能力目录、不修改任何既有能力的 requirement，但整体更换了存储引擎与数据目录格式（旧数据目录不再可读）。按版本规则中"整体更换基础组件计为 Major"的条款升主版本。

## Impact

- `pkg/storage`：新增 SQLite 实现与查询层；删除 JSON 文件实现；`SearchMessages` 的结果顺序与游标语义改变。
- `go.mod`：新增 `modernc.org/sqlite` 依赖（约数 MB 编译产物体积增长，无 cgo 要求）。
- `cmd/link-chat-server`：打开存储的函数名与数据目录内的文件布局改变（`users.json` 等变为 `linkchat.db` 加 WAL 附属文件）。
- `pkg/dirlock`：不变，独占仍作用于数据目录。
- 运维：数据目录的备份对象从多个 JSON 文件变为一个数据库文件加 `-wal`/`-shm`；不需要数据迁移，旧数据目录可以丢弃重建。
