# Tasks

## 1. 活动数据

- [ ] 1.1 `pkg/server`：`Activity` 结构与四类类别、`activitySummary` 文案；`s.activity(...)` 同时喂 SSE（按权限给完整/脱敏版本）与进程内订阅者（有界、丢最旧）；`SubscribeActivity` 与初始快照
- [ ] 1.2 补产出点：好友申请/同意/删除/拉黑、动态发布/点赞/评论/删除、频道新建/加入；现有上线下线、注册登录、消息、处分改为也产出活动
- [ ] 1.3 后台接口 `/admin/api/live/snapshot`（`stats.view`）：用户、在线、最近活动时间、统计、频道（名称仅 `chat.view`）

## 2. 终端仪表盘

- [ ] 2.1 `pkg/server/live`：布局、统计栏、用户列表（在线/离线、按最近活动、「还有 N 人」）、频道框的出现与淡出
- [ ] 2.2 飞点动画：折线路径、拖尾、并行上限、目标不在屏幕时飞向「还有 N 人」
- [ ] 2.3 底部分类日志：着色标签、滚动、技术日志并入「系统」类
- [ ] 2.4 `cmd/link-chat-server`：交互终端检测、`-plain` 参数、日志接线、`q`/`Ctrl+C` 优雅关停与终端恢复

## 3. 后台实况页

- [ ] 3.1 内嵌 three.js 固定版本（`adminweb/vendor/`，记录版本来源）；导航新增「实况」（`stats.view`）
- [ ] 3.2 场景：星空、用户星体（在线辉光/离线暗淡、按活跃度排布）、频道星环、Bloom
- [ ] 3.3 消息流星：弧线飞行、粒子拖尾、抵达爆散；有 `chat.view` 时的内容气泡
- [ ] 3.4 其他活动特效与侧栏（实时统计、分类事件流）、`prefers-reduced-motion`

## 4. 确认后：测试、文档与收尾

（以下在用户看过效果并确认后进行）

- [ ] 4.1 测试：活动产出与权限过滤（只有 `stats.view` 收不到内容）、快照接口鉴权、仪表盘用 SimulationScreen 测试列表排序与「还有 N 人」、非终端不启用
- [ ] 4.2 `docs/server.md`（或现有服务端文档）新增「实况仪表盘」一节；`docs/` 后台文档新增「实况」页
- [ ] 4.3 根目录 `VERSION` 改为 `6.0.0`，`CHANGELOG.md` 新增 6.0.0 一节
- [ ] 4.4 全量 `go vet ./...`、`gofmt -l`、`go test ./...` 通过

## Workflow follow-up

- 实现完成后归档本变更，新建 `server-live` 主 spec，同步 `server-runtime`、`admin-console` 的修改。
