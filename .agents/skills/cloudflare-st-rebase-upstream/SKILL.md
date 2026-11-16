---
name: cloudflare-st-rebase-upstream
description: 把 CloudflareST 本地 main 的独有提交安全重放到已核验的 masgzy/CloudflareST main 之上，含完整等价提交排除、语义冲突合并与带租约自动发布；已启用 Cindy 每日定时同步（每天 09:40 Asia/Shanghai，可暂停）。仅在用户明确要求同步上游或运行本项目技能时调用；不用于立即执行普通变基、不用于同步 XIU2/CloudflareSpeedTest 原项目。
metadata:
  short-description: CloudflareST 专用的上游重放、冲突处理与受约束发布流程
---

# CloudflareST 上游 Rebase 技能

本技能只适用于**这个仓库**。它把本地 `main` 的独有提交重新接到直接父仓库 `masgzy/CloudflareST` 的 `main` 之上，之后按已授权的策略验证并发布到个人 fork。生成于 2026-11-13；生成时只做了只读识别，没有执行真实 rebase、stash 或 push。定时任务于同日单独授权启用、首次触发 2026-11-14 09:40（见第 10 节）。

## 1. 项目身份（已核验，不要替换成模板地址）

| 角色 | 值 |
| --- | --- |
| 仓库根 | `/Users/lazzman/Codes/Code/Demo/cursor/运维/CF优选IP/CloudflareST` |
| 项目标识 | `go.mod` 中的 `module github.com/masgzy/CloudflareST` |
| 上游（重放基线） | `https://github.com/masgzy/CloudflareST.git`，分支 `main` |
| 个人 fork / 发布目标 | `https://github.com/lazzman/CloudflareST.git`，分支 `main` |
| 本地目标分支 | `main`（当前检出分支；不是它就停止，不切换分支） |
| 上游跟踪引用 | `refs/remotes/rebase-upstream/main`（脚本抓取时使用，不写 `remote.*` 配置） |

生成时的只读核验证据：`masgzy/CloudflareST` 默认分支为 `main`、HEAD 为 `75bd076f42b3d5cf8974ebe611f3bd92a85ff71f`，与本地 HEAD 相同；`lazzman/CloudflareST` 是 fork，parent 为 `masgzy/CloudflareST`；后者又 fork 自 `XIU2/CloudflareSpeedTest`（默认分支 `master`）。链条是 **XIU2 → masgzy → lazzman**，本技能的基线是中间那一层，不是最上游的 XIU2。

远程名不等于仓库所有权：本地只有一个 `origin`（指向 lazzman），没有名为 `upstream` 的远程。脚本通过**已核验的固定 URL** 抓取基线，绝不把 `origin` 当上游，也绝不把 `UPSTREAM_URL` 当推送目标。

## 2. 何时使用 / 何时不用

**使用**：「同步上游」「把我的魔改重放到 masgzy/CloudflareST 最新提交上」「检查有没有上游增量」「上游更新后处理冲突」。

**不使用**：

- 只想对当前分支做一次普通变基（用客户端的 Git Rebase 执行工作流，不要用本技能生成或改写策略）。
- 想同步 `XIU2/CloudflareSpeedTest` 或 `GuangYu-yu/CloudflareST-Rust`：基线不同，需要另建技能或单独流程，不能改掉这里的 URL 硬编码。
- 想把本地未推送的提交交给 CI 处理：CI 读不到本机工作区。
- 目标分支是多人协作的共享分支：当前授权基于「lazzman/CloudflareST.git 的 main 为个人独占」这一前提，前提变化必须先改策略。
- 工作区有进行中的 rebase / merge / cherry-pick、detached HEAD、目标分支被另一个 worktree 检出时：脚本会自行停止，不要绕过。

## 3. 调用方式

所有命令都在仓库根目录执行；脚本路径基于本技能目录，不依赖任何客户端或用户主目录资源。

```bash
SKILL=.agents/skills/cloudflare-st-rebase-upstream

# 只读检查：抓取上游并报告是否有增量，不改工作区、不推送
bash "$SKILL/scripts/rebase_upstream.sh" --check

# 正式执行（策略已授权：验证通过后带租约自动发布到 origin/main）
bash "$SKILL/scripts/rebase_upstream.sh" --publish=auto-lease

# 只在本地演练 / 只给出发布建议
bash "$SKILL/scripts/rebase_upstream.sh" --publish=never
bash "$SKILL/scripts/rebase_upstream.sh" --publish=advise

# 智能模式：逐条排除经证据卡核验为完整等价的本地提交（可重复 --exclude）
bash "$SKILL/scripts/rebase_upstream.sh" --publish=never \
  --exclude <提交哈希> --evidence /path/to/evidence.txt

# 冲突已由 Agent 语义合并并 git add 后继续
bash "$SKILL/scripts/rebase_upstream.sh" --resume --publish=never

# 隔离回归测试（只用临时 Git 仓库，不接触真实远程）
bash "$SKILL/scripts/test_rebase_upstream.sh"
```

省略 `--publish` 时取策略授权值 `auto-lease`，即验证通过后自动带租约发布；建议显式写出 `--publish=auto-lease` 让每次执行的意图可见，`--publish=never` 可随时关闭发布。脚本永远不会推送到 `upstream`，`--check` 也永远不推送。

## 4. 最终策略（逐项可追溯）

| 项 | 值 | 来源 |
| --- | --- | --- |
| profile | 聪明省心 | 用户确认 |
| duplicate_commits | `drop-verified`：仅在完整等价 + 证据卡时精确排除**单个**提交 | 用户明确授权 |
| conflict_resolution | `semantic-verified`：Agent 读三方版本语义合并，互斥实现/未知二进制/验证失败即停 | 用户明确选择 |
| worktree_strategy | `stop-on-other-branch`：不切换、不清理其他 worktree | 安全默认 |
| publish | `auto-lease`：固定 `origin/main`、`--force-with-lease` 显式租约、验证不过禁推 | 用户明确授权 + 独占确认 |
| schedule | `install-authorized`：Cindy 原生定时 Agent，每天 09:40 `Asia/Shanghai`，无变化静默跳过 | **你明确授权启用 + 已回读核验 active** |
| git_config | `unchanged`（不写 `remote.*` / `branch.*` / `rerere`） | 安全默认，未授权 |
| validation | `scoped`：Git 完整性检查 + `go test ./...` | 安全默认 |
| notifications | 仅实质变化或阻塞时报告；无更新静默跳过 | 安全默认 |
| 工作区保护 | stash 含未跟踪文件 + 稳定哈希校验 + 引用移位即停 | 不可削弱的安全契约 |

**保守与省心的差别在这里是执行规则，不是形容词**：保守档不会传 `--exclude`、不会自动排除任何提交、冲突一律保留现场；本技能启用了证据卡驱动的精确排除与 Agent 语义合并，但两者都以测试与证据为门禁——脚本本身不理解业务，没有证据卡就一律保留全部提交，遇到冲突一律保留现场。

## 5. Agent 执行流程（语义部分）

脚本只做确定性 Git 流程；下面两步必须由调用本技能的 Agent 完成。

1. **先跑 `--check`**，看 `UP_TO_DATE` 与 `ORIGIN_STATE`。`UP_TO_DATE=yes` 时不改写 HEAD、不创建 stash、不推送。
2. **完整等价判定**（仅在确实想跳过重复提交时）：对 `merge-base..HEAD` 中每条待重放提交做证据卡，写到一个文本文件，格式：

   ```
   COMPLETE_EQUIVALENCE <提交哈希> <上游已完整提供该功能的用户可见证据>
   PARTIAL_COVERAGE     <提交哈希> <只覆盖了哪一部分、还缺什么>
   INSUFFICIENT_EVIDENCE <提交哈希> <为什么无法判定>
   DEPENDENT_COMMIT     <提交哈希> <依赖了哪些其他本地提交>
   ```

   证据要覆盖：提交意图与原始用户可见功能、上游对应的路由/配置项/数据类型/错误码/可见行为、测试证据、是否有依赖该提交的本地提交、是否会间接丢掉更早的独有功能。**不得**用 `git patch-id`、提交信息、作者、文件交集或 `git rebase` 自动跳过补丁来替代业务等价判断。只对标记 `COMPLETE_EQUIVALENCE` 的提交传 `--exclude`；部分覆盖、证据不足、merge 提交、大范围 `--onto` 一律停止人工审查。

3. **运行脚本**。脚本会保存原始 HEAD、校验每条 `--exclude` 在待重放范围内且有完整等价证据、用受控交互式 todo 精确 `drop` 指定提交，并在重放后用 `git range-diff` 与提交覆盖检查确认没有误删其他本地提交。
4. **遇到 `RESULT=CONFLICT`（退出码 20）**：逐文件读取三方版本，语义合并保留上游修复与本地独有能力，然后 `git add` 并运行 `--resume`。三方版本命令：

   ```bash
   git show :1:<path>   # 共同祖先
   git show :2:<path>   # 上游基线（rebase 中的 --ours）
   git show :3:<path>   # 正在重放的本地提交（rebase 中的 --theirs）
   ```

   注意 rebase 中 `--ours` 是**目标上游基线**，`--theirs` 是**本地独有提交**，与 merge 相反。互斥的产品选择、来源不明的二进制、子模块指针无法确认兼容版本、或验证不充分时**停止并交人工**，不要继续合并。
5. **完成后核对报告字段**（见第 7 节），特别是 `RESULT`、`PUBLISH_RESULT`、`STASH_STATE`、`ORIGINAL_HEAD`、`REBASED_HEAD`。

## 6. 停止线与禁止动作

**任一情况立即停止**：上游 `fetch` 失败或引用无法解析（不得回退到过期引用，也不得先 stash）；项目标识 / `origin` URL 与批准值不符；进行中的 rebase / merge / cherry-pick / revert；detached HEAD；目标分支被其他 worktree 检出；本地与上游无共同祖先；待重放历史含 merge 提交；stash 引用移位；`git diff --check` / `git fsck` / 项目测试失败；自动发布的目标 URL、独占确认或租约任一未通过。

**禁止**：`git reset --hard`、`git clean -fd`、`git rebase --skip`、`git rebase --abort`、`git push --force`（普通强推）、整文件 `git checkout --ours/--theirs` 作为冲突解法、向 `upstream` 推送、覆盖未提交工作、切换用户正在编辑的分支、修改持久 Git 配置、擅自创建或修改定时任务、在定时环境中等待交互式输入。

发布失败时保留本地结果并报告，**绝不**退回普通 `--force` 或自动重试。脚本的 `push` 目标硬编码为 `origin` 的固定分支，且执行前复核 URL 与远端引用。

## 7. 报告字段与退出码

每次运行（包括意外 shell 失败）都在 stdout 输出 `KEY=VALUE` 结构化报告，日志在 stderr：

| 字段 | 含义 |
| --- | --- |
| `RESULT` | `SUCCESS` / `CONFLICT` / `STASH_CONFLICT` / `FAILED`，状态未知绝不伪装成功 |
| `PHASE` | `preflight` / `fetch` / `verify-upstream` / `stash` / `rebase` / `verify` / `publish` / `restore-stash` / `complete` |
| `STASH_STATE` | `none` / `pending` / `restored` / `conflict` / `reference-changed` |
| `RESTORE_STASH_REQUIRED` | `no` / `yes` / `manual` |
| `ORIGINAL_HEAD` / `REBASED_HEAD` | 重放前后 HEAD，用于人工恢复 |
| `STASH_REF` / `STASH_COMMIT` | stash 引用与**稳定对象哈希** |
| `EXCLUDED_COMMITS` / `CONFLICT_FILES` / `REBASE_HEAD` | 排除记录、冲突文件、正在重放的提交 |
| `PUBLISH_MODE` / `PUBLISH_RESULT` / `ORIGIN_STATE` | 发布模式、发布结果、origin 同步状态 |
| `UP_TO_DATE` / `NEXT_STEP` / `NOTES` / `FAIL_REASON` | 是否有增量、安全的下一步、备注、失败原因 |
| `BUSY` | `yes` 表示另一个运行持锁，本轮不重入 |

退出码：`0` 成功或无更新、`20` 冲突待处理、`21` stash 恢复冲突、`22` 已有运行持锁、`1` 其他失败。

## 8. 人工停止与恢复

- **冲突现场**：脚本保留 `.git/rebase-merge`（或 `rebase-apply`）、未合并索引、三方内容与 `REBASE_HEAD`；未自动恢复 stash、未推送。用第 5 节的命令读取三方版本，合并并 `git add` 后 `--resume`；要放弃则**由人自行决定**，脚本不会替你 `--abort`。
- **取回原始历史**：用 `git switch -c recovery/<日期> <ORIGINAL_HEAD>` 从原始 HEAD 新建分支，不要用 `git reset --hard` 覆盖当前工作。
- **stash 恢复**：`STASH_STATE=reference-changed` 时按报告里的 `STASH_COMMIT` 定位：`git stash list`、`git stash apply <STASH_COMMIT>`，确认无误后再 `git stash drop`；不要盲目 `stash pop`。`STASH_STATE=conflict` 时保留 stash 与三方冲突现场，人工解决后清理。
- **发布失败**：本地 rebase 结果保留；核对 `origin/main` 的实际状态与租约后，用 `git push --force-with-lease origin HEAD:refs/heads/main` 手动发布，不改脚本去降低门禁。

## 9. 验证与交付状态

- 脚本内置：`git status --short`、`git diff --check`、`git log <upstream>..HEAD`、`git range-diff`（适用时）、`git fsck --no-reflogs --no-progress`、未解决冲突与 stash 状态检查、`go test ./...`。
- 结构检查：`bash -n scripts/*.sh`；本机没有 `shellcheck` 与 `quick_validate.py`，这两项未执行。
- 隔离回归：`bash scripts/test_rebase_upstream.sh` 覆盖 23 组场景、105 项断言（无更新、正常重放、stash、fetch 失败、错误远程、Git 状态阻断、文本/二进制冲突、`--resume`、advise/授权/失败发布、验证失败禁推、精确排除、部分覆盖、无证据、stash 引用移位、意外失败、origin 不同步、无持久配置改动、并发忙碌、陈旧锁恢复）。**租约并发竞争是用 pre-receive 拒绝模拟的，未做真实并发强推测试。**
- 定时前置检查脚本已在项目根目录自测：`exit 2`（本地已包含上游且 origin 同步 → 跳过本轮），且自测前后仓库状态未变。
- 未验证：定时任务真实触发尚未发生（未执行 `run_now`）、定时 Agent 端到端执行与自动发布未做真实演练、`shellcheck` / `quick_validate.py` 本机不可用。
- 真实仓库与真实远程**未执行 rebase / stash / push**，Git 配置未变；定时任务已在 Cindy 中创建并回读为 `active`（本次唯一新增的外部状态），但尚未触发。生成技能与创建定时都不等于执行同步。

## 10. 定时运行（已启用）与跨客户端使用

**当前定时任务（Cindy 原生，已回读核验）**

| 项 | 值 |
| --- | --- |
| 任务名 / ID | `CloudflareST 每日上游 rebase` / `7996552c-9a2d-457a-a678-2cb532640bfe` |
| 计划 / 状态 | 每天 09:40 `Asia/Shanghai`（`40 9 * * *`）；`active`，首次触发 2026-11-14 09:40 |
| 运行位置 | 本机，工作目录 = 本仓库根；`useWorktree=false`，不切分支 |
| 执行引擎 | Cindy 原生定时 Agent（codex / `grok2api/grok-4.6` / medium），非脚本模式 |
| 前置检查 | 只读校验身份、分支、Git 状态与上游/个人远程真实引用；无增量且 origin 同步 → 跳过本轮（零 token），网络或引用异常 → 阻止本轮并记录失败 |
| 重入保护 | 脚本内 `.git/rebase-upstream.lock`；并发运行 `BUSY=yes` 退出 22 |
| 通知 | 无变化静默；实质变化或阻塞时提醒（桌面通知开、飞书关） |
| 执行内容 | 调用本技能 + `--publish=auto-lease`；完整等价排除、语义合并、带租约发布均沿用已授权策略 |

任务保存不会立即运行，本次也**没有**手动触发 `run_now`。查看、暂停、恢复、改时间、删除请用 Cindy 的 `schedule_get` / `schedule_pause` / `schedule_resume` / `schedule_update` / `schedule_delete`，按任务 ID 操作；改时间要同时核对时区与 `nextFireAt`。详细部署记录见 `references/rebase-policy.md` 第 9 节。

**跨客户端**：项目内核（`scripts/` 与 `references/`）不依赖 Cindy、Codex、Claude Code 或任何 AI CLI，可独立运行；上面的定时配置只是可选适配层。若某客户端不自动发现仓库内的 `.agents/skills/`，按该客户端的方式把本目录映射或安装为项目技能即可，**不要**为了让某个客户端识别而改写脚本里的身份校验或安全门禁。将来换客户端时只需替换调度层，不必重写 Git 核心；改定时计划或停用也不影响已授权的策略本身。
