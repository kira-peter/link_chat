---
name: git-commit
description: 提交 git 代码并生成规范的 commit 消息。当用户要求提交代码、git commit、提交变更时使用。
---

根据暂存区变更生成规范 commit 消息并提交。

## Step 1：检查工作区状态

并行运行以下命令：
- `git status`
- `git diff --stat`
- `git branch --show-current`

根据结果判断：

1. **用户指定了文件**：先执行 `git add <文件>` 暂存
2. **有暂存文件（staged）**：直接进入 Step 2
3. **无暂存但有未暂存变更**：列出未暂存文件，使用 **AskUserQuestion** 询问要暂存哪些
4. **工作区干净**：告知没有需要提交的变更，结束

如果暂存区之外还有其他已修改或未跟踪的文件，**提醒用户**并列出清单，询问是否一并加入。

## Step 2：分析变更内容

运行 `git diff --cached` 查看暂存区实际代码变更，理解改了什么。

## Step 3：生成 commit 消息

严格遵循 CLAUDE.md 中的 Conventional Commits 格式：

```
<type>(<scope>): [任务名] <简要描述>

<正文（可选）>
```

**type**（必选其一）：

| type | 用途 |
|------|------|
| feat | 新功能 |
| fix | 修复 bug |
| refactor | 重构（不改功能） |
| perf | 性能优化 |
| docs | 文档 |
| chore | 构建/工具/依赖 |
| test | 测试 |

**规则**：
- **scope**：填模块名（如 `game`、`web`、`protocol`、`dao`），不填单号
- **[任务名]**：有明确任务时用方括号写中文任务名；无任务时省略
- **描述**：中文，不超过 50 字
- **正文**：多处改动时用 `-` 列举关键变更

## Step 4：展示并确认

将生成的 commit 消息展示给用户确认，等待用户批准后再执行提交。

## Step 5：提交

```bash
git commit -m "$(cat <<'EOF'
<commit message>
EOF
)"
```

提交后运行 `git status` 验证成功。

## 示例

简单提交：
```
fix(dao): 修复 RolesEntityDao 内存索引不一致问题
```

带任务名：
```
feat(game): [联盟捐献] 新增联盟资源捐献功能

- 新增 AllianceDonateHandler 处理捐献请求
- DonateService 实现捐献逻辑和冷却计算
```

带正文的重构：
```
refactor(game): 重构活动数据下发逻辑

- 抽取公共下发接口到 ActivityPushService
- 删除各模块重复的推送代码
```
