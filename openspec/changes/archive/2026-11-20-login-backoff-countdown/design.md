# Design

## Context

见 proposal.md「Why」。现有的 `rateLimiter`（`pkg/server/auth.go`）同时承担两件事：滑动窗口的请求频率限制（`login_attempts_per_minute`），以及失败后的指数退避（1 秒起、上限 5 分钟）。它的失败计数放在内存里，按来源地址累加，服务端重启即清零。

已有 `rate_limited` 错误码与 `retry_after_ms` 字段（`pkg/protocol/frame.go`），客户端目前只是把 `rate_limited` 映射成一句固定文案。客户端的定时器 `tick` 每 250 毫秒运行一次（`pkg/app/app.go`），足够驱动倒计时的秒级显示。

## Goals / Non-Goals

**Goals:**

- 等待阶梯与 iPhone 一致，但第 10 次起按天翻倍而不是永久锁定。
- 计数按「来源 IP + 用户名」分开，并在重启后保留。
- 客户端把剩余的等待时间显示成倒计时，并在归零前拒绝提交。

**Non-Goals:**

- 不实现「抹除账号」或永久锁定。
- 不改找回密码、注册与后台登录的规则。
- 不改变客户端「使用已保存会话登录」的路径（恢复失败仍是令牌问题，不是密码问题）。

## Decisions

### 1. 新建独立的失败计数组件，而不是改造 `rateLimiter`

新增 `pkg/server/loginbackoff.go`，负责「账号级」的等待阶梯与持久化；`rateLimiter` 保留，只承担每分钟的突发限制。两者串联：先过频率限制，再查等待阶梯。

理由：`rateLimiter` 的语义是「来源」维度的流量整形，被找回密码与后台登录共用；把账号级、会持久化、规则完全不同的阶梯塞进去，会让三处调用方都拿到不适用的行为。分开后，找回密码与后台登录的代码一行不动。

替代方案：给 `rateLimiter` 加模式开关。否决——两种规则的生命周期与存储要求都不同，开关会让每次改动都要重新核对三处调用方。

### 2. 键为 `来源 IP + 用户名`

`key = hostOnly(remote) + "\x00" + lowercase(username)`。`hostOnly` 已有（`admin_auth.go`），去掉端口，避免每次重连都换一个计数。

只按 IP 会让同一出口后面的人互相牵连；只按用户名会让任何人故意输错把别人锁一天。组合起来两种误伤都没有，代价是攻击者换 IP 可以重置计数——这一层由每分钟请求上限和递增等待共同兜底。

### 3. 阶梯函数是纯函数，单独测试

```go
func backoffDelay(failures int) time.Duration // 0 for 1..3, then the table, doubling daily, capped at 365d
```

表驱动实现：前 3 次返回 0，第 4–9 次查定值，第 10 次起 `24h << (failures-10)`，上限 365 天（防止溢出成负数而意外放行）。

### 4. 持久化到 `data/login_failures.json`

结构：`{"entries":{"<key>":{"failures":N,"block_until":MS,"updated_at":MS}}}`，写入复用 `storage` 包已有的原子写入方式（临时文件 + rename），由 `loginbackoff` 自己持锁、自己落盘，不经过存储 actor——它只被服务端进程使用，而数据目录已由 `dirlock` 保证单进程独占。

写入策略：每次失败或成功清零时写一次，文件很小（每个活跃账号几十字节）。启动时读取，丢弃 `block_until` 已过且 `updated_at` 早于 30 天的条目。

替代方案：放进 `storage.Storage` 接口。否决——这会让存储接口多出与聊天数据无关的职责，测试替身也要跟着实现。

### 5. `Allow` 在阶梯生效时返回剩余时间

`loginbackoff.Check(key) (wait time.Duration, ok bool)`：等待中的 key 返回剩余的精确时长，客户端据此显示倒计时；未等待时返回 0。`RecordFailure(key)` 递增并计算 `block_until`；`RecordSuccess(key)` 删除该条目。

登录路径（`opLogin`）的顺序：解析请求 → 频率限制 → **等待阶梯检查**（在密码校验之前，目的是不让攻击者用猜测结果探测）→ 查用户 → 校验密码。

### 6. 客户端倒计时放在登录面板模型里

`views.LoginModel` 增加 `RetryAt int64`（毫秒时间戳，0 表示不等待）。`submitLogin` 遇 `rate_limited` 时把 `retry_after_ms` 换成 `RetryAt = now + ms`，错误行由 `tick` 每秒重绘成「等待 4 分 12 秒后可重试」；归零时清空 `RetryAt` 并恢复普通提示。倒计时期间 Enter 在 `submitLogin` 入口直接返回。

用绝对时间戳而不是剩余秒数，避免 tick 漏拍导致的漂移；也不需要额外的定时器。

## Risks / Trade-offs

- **按 IP + 用户名计数，攻击者换 IP 或换账号可以扩散尝试** → 保留每分钟请求上限；阶梯本身会让每个组合的尝试成本迅速上升。
- **持久化文件被手工删除即可解锁** → 属于管理员可以直接改数据目录的场景，与重置密码同级，不额外防护；审计日志不涉及。
- **1 天的等待对忘记密码的用户很重** → 客户端文案明确写出剩余时间；找回密码流程（安全问题）是另一条路径，可以重置密码后重新登录；管理员也可用 `-reset-password`。
- **从 5 分钟上限改成 8 小时/1 天，可能挡住真实用户的正常重试** → 只在连续 9 次以上错误时发生，前 3 次完全不受限，之前的规则也已在第 5 次开始惩罚。
- **旧的内存计数不迁移** → 升级即清零，属于放宽而非收紧，没有用户被意外锁住。

## Migration Plan

服务端升级后首次启动没有 `login_failures.json`，按空计数运行。回滚只需换回旧二进制；残留的文件会被旧版本忽略，可直接删除。
