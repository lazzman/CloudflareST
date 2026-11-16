# CloudflareST 上游 Rebase 策略记录

生成日期：2026-11-13；同日追加定时启用记录（第 9 节）。本文件记录「为什么这么生成」「哪些是事实、哪些是你确认的、哪些只是安全默认」，以及将来如何变更策略。生成与定时启用过程都没有在真实仓库执行 rebase、stash、push，也没有修改 Git 配置；定时任务已按单独授权创建并回读核验。

## 1. 决策记录

```yaml
repository: /Users/lazzman/Codes/Code/Demo/cursor/运维/CF优选IP/CloudflareST
repo_marker: "go.mod -> module github.com/masgzy/CloudflareST"
upstream: https://github.com/masgzy/CloudflareST.git refs/heads/main
upstream_verification: 生成时 ls-remote 得到 HEAD 75bd076f42b3d5cf8974ebe611f3bd92a85ff71f，默认分支 main
origin: https://github.com/lazzman/CloudflareST.git
target_branch: main
worktree_strategy: stop-on-other-branch
profile: smart
duplicate_commits: drop-verified
conflict_resolution: semantic-verified
publish: auto-lease
publish_target: origin refs/heads/main（用户确认个人独占）
schedule: install-authorized
scheduler_backend: current-client（Cindy 原生定时 Agent 任务，本机）
scheduler_target: Cindy 定时任务 7996552c-9a2d-457a-a678-2cb532640bfe；工作目录 /Users/lazzman/Codes/Code/Demo/cursor/运维/CF优选IP/CloudflareST
automation_executor: current-client-agent（Cindy 原生定时 Agent，codex / grok2api/grok-4.6 / medium）
git_config: unchanged
validation: scoped
notifications: changes-only（silentWhenIdle=true，桌面通知开、飞书关）
authorization:
  repository: 仓库事实（git rev-parse / status / remote -v / worktree list）
  upstream: 用户明确选择 masgzy/CloudflareST 作为重放基线
  origin_exclusive: 用户明确确认 lazzman/CloudflareST.git main 为个人独占
  profile: 用户确认「聪明省心」
  duplicate_commits: 用户针对「删除提交」单独明确授权 drop-verified
  conflict_resolution: 用户针对「语义自动合并」单独明确选择
  publish: 用户针对「自动发布」单独明确授权 auto-lease，并完成独占确认
  drop-verified / auto-lease / install-authorized / explicitly-allowed: 前三项已授权（定时任务于 2026-11-13 单独授权、创建并回读核验）；持久配置改动仍未授权
  schedule_plan: 用户确认「每天 09:40 Asia/Shanghai」；后端、执行引擎与静默策略沿用既有同类任务默认
  target_branch / git_config / validation / notifications: 安全默认，未逐项询问
```

## 2. 项目事实与只读核验证据

- 仓库根即当前工作目录，`git rev-parse --show-toplevel` 唯一；`main` @ `75bd076`，工作区干净；无 rebase / merge / cherry-pick / revert 进行中，非 detached HEAD，单 worktree。
- 本地只有一个远程 `origin = https://github.com/lazzman/CloudflareST.git`，跟踪 `origin/main`；**没有**名为 `upstream` 的远程。
- README / statement / main.go 声明本项目基于 `XIU2/CloudflareSpeedTest` 修改；go.mod 与徽章、Releases 指向 `masgzy/CloudflareST`；历史里有「同步上游 d129d78」「同步 65b43aa」等提交，共 461 个提交。
- 只读 `git ls-remote`：`masgzy/CloudflareST` 默认分支 `main`，HEAD 与本地 HEAD 相同（生成时无上游增量）；另有分支 `fix-tl-filter-break-bug`。
- 只读 GitHub API：`lazzman/CloudflareST` 是 fork，parent = `masgzy/CloudflareST`；`masgzy/CloudflareST` 也是 fork，parent = `XIU2/CloudflareSpeedTest`（默认分支 `master`）。
- 结论：链条为 `XIU2/CloudflareSpeedTest → masgzy/CloudflareST → lazzman/CloudflareST`。你选择的是**直接父仓库**作为重放基线，而不是最上游的 XIU2。这解释了「差异可能更小、但同步价值有限」的取舍。

**远程名不等于仓库所有权**：脚本不按远程名判断用途，而是把上游固定为已核验 URL、把发布目标固定为 `origin`，并在每次执行前复核 URL。

## 3. 授权边界（不要把档位当成通用授权）

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| 完整等价时自动排除单个提交 | 已授权 | 需要证据卡、精确范围、原始 HEAD、排除记录、`git range-diff` 与提交覆盖检查 |
| 按业务语义自动合并冲突 | 已授权 | 由 Agent 读三方版本完成；脚本不理解业务，只保留现场并提供 `--resume` |
| 验证通过后自动推个人 fork | 已授权 | 固定 `origin/main`、显式租约 `--force-with-lease=<ref>:<远端OID>`、验证不过禁推、失败不重试普通 `--force` |
| 创建定时任务 | **已单独授权并启用** | 2026-11-13 用户明确要求「继续启用定时任务」并确认每天 09:40 Asia/Shanghai；已创建 Cindy 原生任务、回读为 active，详见第 9 节 |
| 修改持久 Git 配置 | **未授权，未启用** | 不写 `remote.*` / `branch.*` / `rerere`；抓取用固定 URL + 精确 refspec |
| 推送到 `upstream` | **永久禁止** | 脚本的 push 目标硬编码为 `origin` 的固定分支 |

选择「聪明省心」或「自动发布」都不隐含定时、配置改动或删除提交的授权；上面每一行都是单独确认或明确的安全默认。

## 4. 未验证项与已知限制

- **未在真实仓库执行 rebase / push**：所有行为验证都在 `mktemp` 临时仓库与本地裸远程中完成。
- **测试能否验证「功能契约」尚未核验**：项目有 6 个 `*_test.go` 与 `.github/workflows/test.yml`，但覆盖率未评估。`drop-verified` 与 `semantic-verified` 都以验证充分为门禁；不足时执行会降级为停止并升级，不会硬判。
- **origin 独占性是你的确认，不是技术核验**：GitHub 协作者列表未被读取；若该分支变成多人协作，必须先改本策略。
- **租约并发竞争未真实模拟**：验收中用 `pre-receive` 拒绝模拟发布失败，验证了「只尝试一次、不退回普通 `--force`、保留本地结果」；真实并发租约冲突未测。
- **上游 URL 的等价形式**：脚本接受等价的合法 Git URL 表达（协议 / `.git` 后缀 / scp 风格），但未逐一实测所有形式。
- **定时任务真实触发尚未发生**：任务保存不会立即运行，首次计划触发 2026-11-14 09:40（Asia/Shanghai）；未执行 `run_now`，因此「定时 Agent 端到端执行 + 自动发布」未做真实演练。
- **定时重入仅在隔离测试中验证**：项目锁与 `BUSY` 退出码已测，但未在两个真实触发重叠时验证。
- **CI 路径**：未启用，主机、凭据与 runner 均未确定，也没有生成 `agents/openai.yaml` 或 CI 调度配置；CI 读不到本机未推送提交，不适用于本仓库的本地 rebase。
- **静态检查工具**：本机无 `shellcheck`、无 `quick_validate.py`；已执行 `bash -n` 与隔离回归测试。

## 5. 完整等价的判定方法（不可简化）

「完整等价」指上游已经提供了该本地提交的**全部用户可见功能**，不是文件相同、路径重叠、补丁相似或只覆盖部分逻辑。逐条评估：

1. 提交标识、父提交、改动意图、原始用户可见功能；
2. 上游对应的路由 / API、配置项、数据类型、错误码、可见行为，以及测试证据；
3. 结论只能是 `COMPLETE_EQUIVALENCE` / `PARTIAL_COVERAGE` / `INSUFFICIENT_EVIDENCE` / `DEPENDENT_COMMIT` 之一，并写明理由；
4. 若拟删除：是否有依赖该提交的本地提交？会不会间接丢掉更早的本地独有功能？
5. 记录授权来源与实际丢弃决策。

禁止仅凭 `git patch-id`、提交信息、作者、文件交集，或 `git rebase` 自动跳过补丁来判定。禁止用 `git rebase --onto <upstream> <某提交> <分支>` 这种大范围写法跳过提交（会连该提交之前的全部本地提交一起丢掉）；脚本使用 `merge-base` 作为范围下界，并用受控交互式 todo 精确 `drop` 指定条目。

## 6. 如何变更策略

| 想改什么 | 改哪里 |
| --- | --- |
| 上游仓库 / 基线分支 | `scripts/rebase_upstream.sh` 顶部 `UPSTREAM_URL` / `UPSTREAM_BRANCH`；同步更新 `SKILL.md` 第 1 节与本文件第 1 节，并重新核验身份与共同祖先 |
| 发布目标或关闭自动发布 | `APPROVED_ORIGIN_URL` / `PUBLISH_AUTHORIZED`；关闭时把标准调用改成 `--publish=never`，并更新 `SKILL.md` 第 3、4 节 |
| 关闭智能排除 / 语义合并 | `DUPLICATE_POLICY` / `CONFLICT_POLICY` 改为 `preserve` / `stop`，并在 `SKILL.md` 中删去证据卡与 `--exclude` 流程 |
| 项目验证命令 | `PROJECT_TEST_CMD`（当前 `go test ./...`）；验证是发布门禁，不可删除 |
| 修改或停用定时 | 已启用（见第 9 节）。用 Cindy 的 `schedule_get` / `schedule_pause` / `schedule_resume` / `schedule_update` / `schedule_delete` 操作任务 `7996552c-9a2d-457a-a678-2cb532640bfe`；改时间须同时核对时区与 `nextFireAt`。Git 核心脚本不应为调度器改写 |
| 修改安全契约 | 先更新本文件与 `SKILL.md` 的停止线，再改脚本，并重新跑 `scripts/test_rebase_upstream.sh` |

变更原则：影响历史或远程的选项必须由用户显式选择；未回答的项一律收敛为明确标记的安全默认，不静默升级。

## 7. 验证命令

```bash
SKILL=.agents/skills/cloudflare-st-rebase-upstream
bash -n "$SKILL/scripts/rebase_upstream.sh"
bash -n "$SKILL/scripts/test_rebase_upstream.sh"
bash "$SKILL/scripts/test_rebase_upstream.sh"          # 隔离回归，只用临时仓库
bash "$SKILL/scripts/rebase_upstream.sh" --check       # 对真实仓库做只读检查，不改工作区
node '/Users/lazzman/Library/Application Support/Cindy/schedule-hooks/cloudflarest-rebase-origin-url-main-git-.mjs'  # 定时前置检查自测：exit 2=跳过 / 0=放行 / 其它=阻止
```

脚本内部验证：`git status --short`、`git diff --check`、`git log <upstream>..HEAD`、`git range-diff`、`git fsck --no-reflogs --no-progress`、未解决冲突检查、stash 恢复状态、`go test ./...`。

## 8. 回滚与人工接管

- 原始历史永远以 `ORIGINAL_HEAD` 报告（脚本不创建备份 ref，避免额外写入 Git 引用；reflog 仍可追溯）。恢复：`git switch -c recovery/<日期> <ORIGINAL_HEAD>`。
- 冲突：保留 `.git/rebase-merge`、三方版本与 `REBASE_HEAD`；语义合并并 `git add` 后 `--resume`，或由人决定如何处理。脚本不执行 `--abort` / `--skip` / `reset --hard`。
- stash：按 `STASH_COMMIT` 稳定哈希定位，`git stash apply <哈希>` 确认后再 drop；`reference-changed` 时绝不盲目 `pop`。
- 发布失败：本地成果保留，核对 `origin/main` 远端引用后手动 `git push --force-with-lease origin HEAD:refs/heads/main`。

## 9. 定时任务部署记录（2026-11-13）

| 项 | 值 |
| --- | --- |
| 后端 | Cindy 原生定时 Agent 任务（本机，回读 scope=local）。创建前已核验 `schedule_create` / `get` / `list` / `pause` / `resume` / `run_now` 与 Agent 执行、工作目录、模型位置、前置检查能力 |
| 任务名 / ID | `CloudflareST 每日上游 rebase` / `7996552c-9a2d-457a-a678-2cb532640bfe` |
| 计划 | `40 9 * * *`，`Asia/Shanghai`，`recurring=true` |
| 状态 / 下次触发 | `active`；`nextFireAt=1794620400000` = 2026-11-14 09:40:00 +08:00（`schedule_get` 回读与创建回执一致） |
| 工作目录 | `/Users/lazzman/Codes/Code/Demo/cursor/运维/CF优选IP/CloudflareST`；`useWorktree=false`，`persistentSession=false` |
| 执行引擎 | agent 模式，`agentKind=codex`，`model=grok2api/grok-4.6`，`providerId=magpie-05fd3950`，`effort=medium`（沿用既有同类任务） |
| 前置检查 | `node '/Users/lazzman/Library/Application Support/Cindy/schedule-hooks/cloudflarest-rebase-origin-url-main-git-.mjs'`，超时 120000ms；exit 0 放行 / 2 跳过本轮（零 token）/ 其它 fail-closed。项目根目录自测得到 exit 2（无增量且 origin 同步），前后仓库状态未变 |
| 静默与通知 | `silentWhenIdle=true`，`notify={desktop:true,feishu:false}`；实质变化或阻塞时由 Agent 调 `schedule_notify_current_run`，无变化调 `schedule_silence_current_run` |
| 重入保护 | 项目脚本 `.git/rebase-upstream.lock`；并发时 `BUSY=yes`、退出 22，不重入；陈旧锁自动清理并记录 |
| 执行授权 | 沿用 `drop-verified` / `semantic-verified` / `auto-lease`；任务 prompt 明确禁止提问、改配置、推 upstream、普通 `--force`、`reset --hard`、`--skip` / `--abort`、整文件选边 |

查看 / 停用 / 恢复（按任务 ID 使用 Cindy 原生能力）：

- 查看：`schedule_get {id}`、`schedule_list {device:"all"}`、`schedule_list_runs`
- 暂停 / 恢复：`schedule_pause {id}` / `schedule_resume {id}`（恢复时 `nextFireAt` 按当时重算，不补触发）
- 改时间：`schedule_update {id, cronExpr, timezone}`，同时核对 `nextFireAt`
- 立即手动触发一次：`schedule_run_now {id}`（独立动作，不改变 cron 节奏）
- 删除：`schedule_delete {id}`；删除前先 `schedule_get` 核对目标与授权

创建时不立即运行；本次没有执行 `run_now`，也没有在真实仓库执行 rebase / push。任务配置属于可选适配层，项目内核不依赖 Cindy；换客户端时只替换调度层。
