# Spec Delta

## MODIFIED Requirements

### Requirement: 设置对象的键与校验

设置对象的键 MUST 为以下之一：`theme`、`motion`、`language`、`bubble_width`、`density`、`time_format`、`msg_border`、`msg_time`、`self_text`、`other_text`、`self_border`、`other_border`、`plaza`、`wheel_key`，值均为字符串。`self_text`、`other_text`、`self_border`、`other_border` 取 `default`/`red`/`orange`/`yellow`/`amber`/`lime`/`green`/`teal`/`cyan`/`sky`/`blue`/`violet`/`pink`/`rose`/`gray`；`plaza` 取 `on`/`off`；`wheel_key` 取 `f1`（默认）/`ctrl_space`/`ctrl_g`/`ctrl_k`/`ctrl_o`。`settings.put` 中出现未知的键或不在候选内的值时，服务端 MUST 返回 `invalid_argument`，且不保存任何项。

#### Scenario: 未知的键
- **WHEN** `settings.put` 载荷包含键 `font_size`
- **THEN** 服务端返回 `invalid_argument`，不保存任何项

#### Scenario: 不在候选内的值
- **WHEN** `settings.put` 载荷中 `bubble_width` 为 `"55"`
- **THEN** 服务端返回 `invalid_argument`，不保存任何项

#### Scenario: 保存快捷转盘按键
- **WHEN** `settings.put` 载荷为 `{"settings": {"wheel_key": "f1"}}`
- **THEN** 服务端保存成功，`settings.get` 返回的 `wheel_key` 为 `"f1"`
