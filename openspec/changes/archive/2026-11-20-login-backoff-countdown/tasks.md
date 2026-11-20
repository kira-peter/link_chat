# Tasks

## 1. 服务端：等待阶梯与持久化

- [x] 1.1 在 `pkg/server/loginbackoff.go` 实现 `backoffDelay(failures int) time.Duration`：1–3 次为 0，4–9 次查表（1 分、5 分、15 分、1 时、3 时、8 时），10 次起 `24h << (failures-10)` 且上限 365 天；用表驱动单测覆盖边界（0、1、3、4、9、10、11、60 次）验证
- [x] 1.2 实现 `loginBackoff` 类型：`Check(key) (wait, blocked)`、`RecordFailure(key)`、`RecordSuccess(key)`，键为 `hostOnly(addr) + "\x00" + strings.ToLower(username)`；单测验证前 3 次不阻塞、第 4 次起返回递减的剩余时间、成功清零
- [x] 1.3 持久化到 `data/login_failures.json`（原子写入），启动时载入并丢弃 `block_until` 已过且 30 天未更新的条目；单测验证写后重开能读回、过期条目被清理、文件损坏时按空计数启动并记一条警告

## 2. 服务端：接入登录路径

- [x] 2.1 在 `opLogin` 中先过频率限制、再查等待阶梯（在查用户之前），等待中返回 `rate_limited` 并带 `retry_after_ms`；失败时 `RecordFailure`，成功时 `RecordSuccess`；用 httptest 的登录用例验证第 4 次失败的 `retry_after_ms` 为 60000，且不同用户名或不同来源互不影响
- [x] 2.2 给 `Server` 增加可配置的 `loginBackoff`（测试注入自定义时钟或临时目录），确认找回密码与后台登录的代码路径未改动、仍走原来的 `loginLimit`；跑 `go test ./pkg/server/...` 全绿
- [x] 2.3 更新 `docs/server.md` 的限流小节：写明阶梯、计数维度、持久化文件与删除文件的后果，并核对文中的命令与文件名

## 3. 客户端：倒计时

- [x] 3.1 `views.LoginModel` 增加 `RetryAt int64`，错误行在 `RetryAt > now` 时显示「等待 X 分 Y 秒后可重试」（不足 1 分钟显示秒，超过 1 小时显示时与分）；视图单测覆盖三种时长格式
- [x] 3.2 `submitLogin` 遇 `rate_limited` 时读取 `retry_after_ms` 并设置 `RetryAt`；`RetryAt` 未到期时 Enter 直接返回不发请求；`tick` 在归零后清空它；e2e 测试：连续输错触发限流后面板出现倒计时，倒计时中按 Enter 不发送，归零后可正常登录
- [x] 3.3 在 `pkg/i18n` 补充中英文倒计时文案，并更新 `docs/client.md` 的「登录失败」说明

## 4. 版本

- [x] 4.1 把根目录 `VERSION` 改为 `2.1.0`，用 `go test ./pkg/version/...` 验证解析结果
