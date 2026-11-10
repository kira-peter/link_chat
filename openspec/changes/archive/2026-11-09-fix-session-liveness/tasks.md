# Tasks

## 1. 协议与存储

- [x] 1.1 新增事件名 `session.replaced` 与载荷 `SessionReplacedEvent{Username, Location, At, Device}`；`AuthRes` 增加 `Device`，`LoginReq` 增加 `Device`，验证编解码单测覆盖
- [x] 1.2 `storage.Token` 增加 `Remote`、`Device`、`LastSeenAt`；新增 `ListTokensForUser(userID)`，验证旧数据缺少字段时正常加载
- [x] 1.3 存储测试覆盖新字段往返与会话令牌列举，验证 `go test ./pkg/storage/` 通过

## 2. 服务端

- [x] 2.1 认证成功时把来源地址与设备名写入令牌记录，验证存储中存在对应字段
- [x] 2.2 登录/注册成功前吊销该用户其他 `session` 令牌（保留 `reset` 令牌），验证旧令牌随后不可用、重置令牌仍可用
- [x] 2.3 通过 Hub 定位被顶掉用户的连接，推送 `session.replaced` 后关闭，验证通知先于断开送达且连接确实关闭
- [x] 2.4 顶号不误伤自身连接，验证同连接重复认证不会把自己踢掉
- [x] 2.5 服务端测试：第二个客户端登录后第一个收到事件并断开、其他账号不受影响，验证事件载荷含 username/location/at

## 3. 客户端配置与心跳

- [x] 3.1 `-as <name>` 参数决定配置路径 `instances/<name>.json`，未指定时用 `default`；老配置自动迁移，验证迁移后无需重新登录
- [x] 3.2 两个不同实例名的客户端可登录不同账号，验证二者配置文件互不覆盖
- [x] 3.3 `Transport` 增加心跳协程，每 `heartbeat/2` 发送 `presence.ping`，验证空闲连接超过读超时仍存活
- [x] 3.4 心跳失败时按断线处理并进入重连，验证真正失联仍能被发现

## 4. 顶号后的客户端行为

- [x] 4.1 收到 `session.replaced` 时停止重连、清除本地令牌、打开提示对话框，验证不进入重连循环
- [x] 4.2 提示对话框显示账号、地点、时间与重新登录入口，验证渲染快照包含这三项
- [x] 4.3 从提示界面重新登录成功后进入主界面，验证端到端对称顶号
- [x] 4.4 新增中英文文案，验证切换语言后提示文本随之更新

## 5. 端到端与文档

- [x] 5.1 端到端测试：两个不同实例登录同一账号，第二个成功、第一个收到顶号提示，验证全链路
- [x] 5.2 端到端测试：空闲超过两个心跳周期连接不断开，验证心跳生效
- [x] 5.3 更新 `docs/client.md`：`-as` 多实例用法、单会话登录说明与顶号提示
- [x] 5.4 更新 `docs/server.md`：心跳与读超时说明、地点信息与 `geoip_db` 配置

## Workflow follow-up

- 评审通过后执行 `openspec archive fix-session-liveness`。
- 归档后运行 `openspec validate --strict --no-interactive`。
