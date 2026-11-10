# Spec Delta

## MODIFIED Requirements

### Requirement: 用户注册

系统 SHALL 允许新用户注册账号，要求提供唯一用户名、密码与安全问题。用户名 MUST 唯一且大小写不敏感地判重，安全问题与答案 MUST 一并保存以便日后找回密码。

#### Scenario: 注册成功
- **WHEN** 新用户提交未被占用的用户名 `alice`、满足强度要求的密码与一个安全问题及答案
- **THEN** 系统创建账号、返回成功响应，且该用户可用同一凭据登录

#### Scenario: 用户名已存在
- **WHEN** 用户提交已存在的用户名 `Alice`（已有账号为 `alice`）
- **THEN** 系统返回 `code` 为 `username_taken` 的响应且不创建账号

#### Scenario: 密码不满足强度要求
- **WHEN** 用户提交长度少于 8 个字符的密码
- **THEN** 系统返回 `code` 为 `weak_password` 的响应且不创建账号

#### Scenario: 用户名格式非法
- **WHEN** 用户提交包含空格或控制字符的用户名
- **THEN** 系统返回 `code` 为 `invalid_username` 的响应并说明允许的字符集与长度范围

#### Scenario: 缺少安全问题
- **WHEN** 用户提交注册请求但没有提供安全问题或答案
- **THEN** 系统返回 `code` 为 `security_question_required` 的响应且不创建账号

#### Scenario: 答案大小写与空白不敏感
- **WHEN** 用户注册时答案为 `Blue Harbor`，之后找回密码时输入 `  blue harbor `
- **THEN** 系统判定答案正确

### Requirement: 认证滥用防护

系统 MUST 对注册、登录与找回密码施加限流与失败退避，防止凭据猜测、账号枚举与重置滥用。

#### Scenario: 连续登录失败触发退避
- **WHEN** 同一来源在短时间内连续 5 次登录失败
- **THEN** 系统对该来源返回 `code` 为 `rate_limited` 的响应并延长每次失败后的等待时间

#### Scenario: 注册频率受限
- **WHEN** 同一来源在 1 分钟内发起超过配置上限的注册请求
- **THEN** 系统拒绝超出部分并返回 `rate_limited`

#### Scenario: 退避后恢复
- **WHEN** 满足退避等待时间后用户以正确凭据登录
- **THEN** 系统正常签发令牌，失败计数重置

#### Scenario: 找回密码错误答案计入退避
- **WHEN** 同一来源连续多次提交错误的找回密码答案
- **THEN** 系统返回 `rate_limited` 并延长等待，与登录失败共用同一套退避规则

## ADDED Requirements

### Requirement: 安全问题存储

安全答案 MUST 以加盐单向哈希形式存储，任何持久化介质中都 MUST NOT 出现答案明文。安全问题本身 SHALL 以明文保存，因为它需要被读出来展示给用户。

#### Scenario: 存储中不含答案明文
- **WHEN** 注册完成后检查服务端数据文件
- **THEN** 文件中不存在安全答案的明文，仅有其哈希与安全问题的文本

#### Scenario: 同一答案产生不同哈希
- **WHEN** 两个用户设置完全相同的安全答案
- **THEN** 二者在存储中的答案哈希不相同

#### Scenario: 老账号没有安全问题
- **WHEN** 查询一个在本功能上线前创建的账号
- **THEN** 该账号没有安全问题，登录不受影响，找回密码时返回 `cannot_recover`

### Requirement: 找回密码流程

系统 SHALL 提供三步找回流程：查询问题、校验答案、重设密码。任一阶段失败 MUST NOT 泄露除该用户名存在问题之外的额外信息。

#### Scenario: 查询安全问题时返回问题
- **WHEN** 用户提交一个存在且设有安全问题的用户名
- **THEN** 系统返回该账号的安全问题文本，不返回其他任何账号信息

#### Scenario: 用户不存在
- **WHEN** 用户提交不存在的用户名查询安全问题
- **THEN** 系统返回 `code` 为 `cannot_recover` 的响应，与"账号无安全问题"的响应完全一致

#### Scenario: 答案正确获得重置令牌
- **WHEN** 用户在 5 分钟内提交正确的安全答案
- **THEN** 系统返回一个重置令牌，并说明其有效期

#### Scenario: 答案错误
- **WHEN** 用户提交错误的安全答案
- **THEN** 系统返回 `code` 为 `security_answer_incorrect` 的响应且不签发重置令牌

#### Scenario: 用重置令牌设置新密码
- **WHEN** 用户携带有效的重置令牌提交满足强度要求的新密码
- **THEN** 系统更新密码、作废该重置令牌，且用户可用新密码登录

#### Scenario: 重置令牌一次性
- **WHEN** 用户用一个已经使用过的重置令牌再次提交新密码
- **THEN** 系统返回 `code` 为 `reset_token_invalid` 的响应

#### Scenario: 重置令牌过期
- **WHEN** 用户使用签发超过 5 分钟的重置令牌
- **THEN** 系统返回 `code` 为 `reset_token_expired` 的响应，用户需回到第一步重新开始

#### Scenario: 新密码不满足强度要求
- **WHEN** 用户用有效令牌提交少于 8 个字符的新密码
- **THEN** 系统返回 `code` 为 `weak_password` 的响应，令牌仍然有效可重试

### Requirement: 重置后的会话吊销

密码重置成功后，系统 MUST 吊销该账号所有已签发的会话令牌，MUST NOT 影响其他账号。

#### Scenario: 重置后旧会话失效
- **WHEN** 用户的密码被成功重置，随后用重置前签发的令牌发起请求
- **THEN** 系统拒绝该令牌并返回 `unauthorized`

#### Scenario: 其他账号不受影响
- **WHEN** 某用户重置了密码
- **THEN** 其他用户已签发的令牌仍然有效

#### Scenario: 重置不影响找回流程的其他参与者
- **WHEN** 两个用户同时在进行找回，其中一个完成重置
- **THEN** 另一个的重置令牌仍然有效直到其自身过期或被使用
