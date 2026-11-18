# Spec Delta

## ADDED Requirements

### Requirement: 最低客户端版本配置

配置 SHALL 提供 `min_client_version`，默认为空表示不限制，并支持环境变量覆盖。设置的值 MUST 是合法的发布版本号，否则服务端拒绝启动并指出该字段。

#### Scenario: 未设置
- **WHEN** 配置中没有 `min_client_version`
- **THEN** 任何版本的客户端、包括不上报版本的旧客户端，都能完成握手

#### Scenario: 值不合法
- **WHEN** `min_client_version` 被设为 `v0.3`
- **THEN** 服务端拒绝启动并指出 `min_client_version` 格式不合法

#### Scenario: 启动日志说明
- **WHEN** 服务端以 `min_client_version` 为 `0.3.0` 启动
- **THEN** 启动日志中记录该最低版本

### Requirement: 记录客户端版本

服务端 SHALL 为每条连接记录握手时上报的客户端版本（未上报时为未知），签发会话令牌时一并保存该版本。登录成功、恢复会话、会话被顶掉、因版本过旧被拒的日志 MUST 包含客户端版本。

#### Scenario: 登录日志带版本
- **WHEN** 版本 `1.0.0` 的客户端登录成功
- **THEN** 登录成功的日志行包含 `client=1.0.0`

#### Scenario: 拒绝时记录
- **WHEN** 客户端因版本过旧被拒
- **THEN** 日志记录来源地址、客户端版本与最低版本
