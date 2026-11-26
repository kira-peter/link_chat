# Spec Delta

## MODIFIED Requirements

### Requirement: 运行日志

服务端 SHALL 记录连接建立与断开、认证失败、限流触发、持久化错误与关停过程。日志 MUST NOT 包含密码、令牌原文或完整消息内容。在交互终端中运行时，日志 SHALL 默认以实况仪表盘的形式呈现（见 server-live）；以 `-plain` 启动或输出不是终端时，MUST 输出与以前相同的普通日志。

#### Scenario: 认证失败被记录
- **WHEN** 某来源登录失败
- **THEN** 日志记录来源、用户名与失败原因，不含尝试的密码

#### Scenario: 日志不含令牌
- **WHEN** 用户登录成功后检查日志
- **THEN** 日志中不出现签发的令牌字符串

#### Scenario: 日志级别可配置
- **WHEN** 运维将日志级别设为更严格的档位
- **THEN** 低级别日志不再输出，错误级别日志仍然输出

#### Scenario: 普通日志模式
- **WHEN** 运维以 `-plain` 启动服务端
- **THEN** 终端只输出普通日志，不显示仪表盘
