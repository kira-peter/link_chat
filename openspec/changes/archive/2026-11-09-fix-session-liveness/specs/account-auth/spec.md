# Spec Delta

## MODIFIED Requirements

### Requirement: 登录与令牌签发

登录成功时系统 SHALL 签发一个不可预测的会话令牌，并使该账号此前的会话失效。令牌 MUST 具有有效期，且 MUST NOT 编码可被客户端伪造的权限信息。

#### Scenario: 登录成功返回令牌
- **WHEN** 用户以正确凭据登录
- **THEN** 系统返回成功响应，包含一个令牌与过期时间

#### Scenario: 凭据错误
- **WHEN** 用户提交正确用户名与错误密码
- **THEN** 系统返回 `code` 为 `invalid_credentials` 的响应，且该错误不区分"用户不存在"与"密码错误"

#### Scenario: 令牌过期后失效
- **WHEN** 客户端使用已过有效期的令牌发起请求
- **THEN** 系统返回 `code` 为 `token_expired` 的响应，连接进入未认证状态

#### Scenario: 令牌被吊销后立即失效
- **WHEN** 用户登出后再次使用同一令牌建立连接
- **THEN** 系统拒绝该令牌，返回 `code` 为 `unauthorized`

#### Scenario: 新登录使旧会话失效
- **WHEN** 用户登录成功，而该账号在其他位置已有活跃会话
- **THEN** 旧会话令牌被吊销，旧连接收到 `session.replaced` 后被关闭

#### Scenario: 令牌记录来源
- **WHEN** 签发会话令牌时能获得来源地址
- **THEN** 该地址随令牌一起记录，供顶号时向被顶掉的一方说明来源
