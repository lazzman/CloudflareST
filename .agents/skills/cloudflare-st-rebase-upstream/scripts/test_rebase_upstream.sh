#!/usr/bin/env bash
# 隔离回归测试：只在 mktemp 临时目录创建工作树与裸远程，绝不接触真实项目的远程或分支。
# 用法: bash scripts/test_rebase_upstream.sh
set -Eeuo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="$HERE/rebase_upstream.sh"
ROOT="$(mktemp -d "${TMPDIR:-/tmp}/cfst-rebase-test.XXXXXX")"
trap 'rm -rf "$ROOT"' EXIT

PASS=0
FAIL=0
SKIP=0
NAME=""
D=""
WORK=""
UP=""
CFG=""
OUT=""
ERR=""
RC=0

ok()   { printf '  ✓ %s\n' "$1"; PASS=$((PASS + 1)); }
bad()  { printf '  ✗ %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() { printf '  - SKIP %s\n' "$1"; SKIP=$((SKIP + 1)); }
title(){ printf '\n[%s]\n' "$1"; }

assert_report() {
  if grep -Fxq "$1=$2" "$OUT"; then
    ok "$3"
  else
    bad "$3 — 期望 $1=$2，实际 $(grep "^$1=" "$OUT" | head -1 || printf '(缺失)')"
  fi
}

assert_out_has() {
  if grep -Fq -- "$1" "$OUT"; then ok "$2"; else bad "$2 — 输出缺少 $1"; fi
}

assert_eq() {
  if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 — 期望 [$2]，实际 [$1]"; fi
}

assert_ne() {
  if [ "$1" != "$2" ]; then ok "$3"; else bad "$3 — 两者不应相等: [$1]"; fi
}

assert_file_has() {
  if grep -Fq -- "$2" "$1" 2>/dev/null; then ok "$3"; else bad "$3 — $1 缺少 $2"; fi
}

write_config() {
  CFG="$D/config.env"
  {
    printf 'APPROVED_ORIGIN_URL=%q\n' "$D/origin.git"
    printf 'UPSTREAM_URL=%q\n' "$D/upstream.git"
    printf 'REPO_MARKER_FILE=%q\n' "go.mod"
    printf 'REPO_MARKER_PATTERN=%q\n' '^module github\.com/masgzy/CloudflareST$'
    printf 'UPSTREAM_BRANCH=%q\n' "main"
    printf 'TARGET_BRANCH=%q\n' "main"
    printf 'TRACKING_REF=%q\n' "refs/remotes/rebase-upstream/main"
    printf 'PROJECT_TEST_CMD=%q\n' "true"
    printf 'PUBLISH_AUTHORIZED=%q\n' "auto-lease"
    printf 'PUBLISH_TARGET_CONFIRMED_EXCLUSIVE=%q\n' "yes"
    printf 'DUPLICATE_POLICY=%q\n' "drop-verified"
    printf 'CONFLICT_POLICY=%q\n' "semantic-verified"
    printf 'TEST_PRE_PUBLISH_HOOK=%q\n' ""
    printf 'TEST_POST_REBASE_HOOK=%q\n' ""
  } > "$CFG"
}

patch_config() {
  printf '%s=%q\n' "$1" "$2" >> "$CFG"
}

make_sandbox() {
  NAME="$1"
  D="$ROOT/$NAME"
  mkdir -p "$D"
  git init --bare -q -b main "$D/upstream.git"
  git init --bare -q -b main "$D/origin.git"
  git -C "$D/upstream.git" symbolic-ref HEAD refs/heads/main
  git -C "$D/origin.git" symbolic-ref HEAD refs/heads/main

  git init -q -b main "$D/seed"
  git -C "$D/seed" config user.email tester@example.com
  git -C "$D/seed" config user.name tester
  printf 'module github.com/masgzy/CloudflareST\n' > "$D/seed/go.mod"
  printf 'base line\n' > "$D/seed/shared.txt"
  printf '\x00\x01BASE' > "$D/seed/blob.bin"
  git -C "$D/seed" add -A
  git -C "$D/seed" commit -qm "base: 初始提交"
  git -C "$D/seed" remote add origin "$D/origin.git"
  git -C "$D/seed" remote add up "$D/upstream.git"
  git -C "$D/seed" push -q origin main
  git -C "$D/seed" push -q up main

  git clone -q "$D/origin.git" "$D/work"
  git clone -q "$D/upstream.git" "$D/up"
  for r in work up; do
    git -C "$D/$r" config user.email tester@example.com
    git -C "$D/$r" config user.name tester
  done
  WORK="$D/work"
  UP="$D/up"
  OUT="$D/out.txt"
  ERR="$D/err.txt"
  write_config
}

run_script() {
  if ( cd "$WORK" && bash "$TARGET" --test-config "$CFG" "$@" ) >"$OUT" 2>"$ERR"; then
    RC=0
  else
    RC=$?
  fi
}

origin_oid() { git ls-remote "$D/origin.git" refs/heads/main | awk 'NR==1{print $1}'; }
work_head()  { git -C "$WORK" rev-parse HEAD; }

upstream_commit() {
  # $1 提交信息, $2 文件名, $3 内容
  printf '%b' "$3" > "$UP/$2"
  git -C "$UP" add -A
  git -C "$UP" commit -qm "$1"
  git -C "$UP" push -q origin main
}

local_commit() {
  printf '%b' "$3" > "$WORK/$2"
  git -C "$WORK" add -A
  git -C "$WORK" commit -qm "$1"
  git -C "$WORK" rev-parse HEAD
}

# ---------------------------------------------------------------- 场景 1：无上游新增
s1_no_upstream_change() {
  title "s1 无上游新增：不改写 HEAD、不创建 stash"
  make_sandbox s1
  local before stash_before
  before="$(work_head)"
  stash_before="$(git -C "$WORK" stash list)"
  run_script --check
  assert_report RESULT SUCCESS "--check 成功"
  assert_report UP_TO_DATE yes "--check 报告已是最新"
  run_script
  assert_report RESULT SUCCESS "完整流程成功"
  assert_report UP_TO_DATE yes "完整流程不改写历史"
  assert_eq "$(work_head)" "$before" "HEAD 未改变"
  assert_eq "$(git -C "$WORK" stash list)" "$stash_before" "未额外创建 stash"
  assert_eq "$(origin_oid)" "$before" "origin/main 未改变"
}

# ---------------------------------------------------------------- 场景 2：正常重放 + 配置不变
s2_rebase_local_commit() {
  title "s2 上游有增量、本地有独有提交：重放并保留本地功能"
  make_sandbox s2
  upstream_commit "upstream: 新增上游文件" upstream_only.txt 'upstream\n'
  local_commit "local: 新增本地独有功能" local_only.txt 'local\n' >/dev/null
  local before cfg_before
  before="$(work_head)"
  cfg_before="$(git -C "$WORK" config --local --list | sort)"
  run_script --publish=never
  assert_report RESULT SUCCESS "重放成功"
  assert_ne "$(work_head)" "$before" "HEAD 已改写"
  assert_report PUBLISH_RESULT disabled "未授权发布时不推送"
  assert_file_has "$WORK/local_only.txt" "local" "本地独有功能仍在"
  assert_file_has "$WORK/upstream_only.txt" "upstream" "上游新功能已合入"
  if git -C "$WORK" merge-base --is-ancestor "$(git ls-remote "$D/upstream.git" refs/heads/main | awk 'NR==1{print $1}')" HEAD; then
    ok "上游提交成为本地 HEAD 的祖先"
  else
    bad "上游提交不是本地 HEAD 的祖先"
  fi
  assert_eq "$(git -C "$WORK" config --local --list | sort)" "$cfg_before" "未修改仓库持久配置"
  assert_eq "$(origin_oid)" "$(git -C "$D/seed" rev-parse HEAD)" "origin/main 未变化"
}

# ---------------------------------------------------------------- 场景 3：stash 保护与恢复
s3_stash_including_untracked() {
  title "s3 未提交/未跟踪文件：stash 后完整恢复，已有 stash 不受影响"
  make_sandbox s3
  printf 'preexisting change\n' >> "$WORK/go.mod"
  git -C "$WORK" stash push -q -m "preexisting" -- go.mod
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 新增本地文件" local_only.txt 'local\n' >/dev/null
  printf 'dirty change\n' >> "$WORK/go.mod"
  printf 'untracked content\n' > "$WORK/new.txt"
  run_script --publish=never
  assert_report RESULT SUCCESS "重放成功"
  assert_report STASH_STATE restored "stash 已恢复"
  assert_report RESTORE_STASH_REQUIRED no "无需人工恢复"
  assert_file_has "$WORK/new.txt" "untracked content" "未跟踪文件已恢复"
  assert_file_has "$WORK/go.mod" "dirty change" "未提交改动已恢复"
  assert_eq "$(git -C "$WORK" stash list | wc -l | tr -d ' ')" "1" "原有 stash 未被删除"
}

# ---------------------------------------------------------------- 场景 4：fetch 失败
s4_fetch_failure() {
  title "s4 上游抓取失败：停止，不先 stash，不回退过期引用"
  make_sandbox s4
  patch_config UPSTREAM_URL "$D/does-not-exist.git"
  printf 'dirty\n' > "$WORK/dirty.txt"
  local before
  before="$(work_head)"
  run_script
  assert_report RESULT FAILED "抓取失败返回 FAILED"
  assert_report PHASE fetch "失败阶段为 fetch"
  assert_report STASH_STATE none "未创建 stash"
  assert_eq "$(work_head)" "$before" "HEAD 未改变"
  assert_file_has "$WORK/dirty.txt" "dirty" "工作区改动未被覆盖"
}

# ---------------------------------------------------------------- 场景 5：远程 URL 不符
s5_wrong_origin_url() {
  title "s5 origin URL 与批准值不符：拒绝执行"
  make_sandbox s5
  git -C "$WORK" remote set-url origin "$D/upstream.git"
  local before
  before="$(origin_oid)"
  run_script
  assert_report RESULT FAILED "拒绝执行"
  assert_report PHASE preflight "失败阶段为 preflight"
  assert_eq "$(origin_oid)" "$before" "未向错误远程推送"
}

# ---------------------------------------------------------------- 场景 6/7/8：Git 状态阻断
s6_in_progress_operation() {
  title "s6 已有进行中的 rebase：拒绝启动"
  make_sandbox s6
  mkdir -p "$WORK/.git/rebase-merge"
  run_script
  assert_report RESULT FAILED "拒绝启动"
  assert_report PHASE preflight "失败阶段为 preflight"
  if [ -d "$WORK/.git/rebase-merge" ]; then ok "原有 rebase 状态被保留"; else bad "原有 rebase 状态被清理"; fi
}

s7_detached_head() {
  title "s7 detached HEAD：拒绝启动"
  make_sandbox s7
  git -C "$WORK" checkout -q --detach HEAD
  run_script
  assert_report RESULT FAILED "拒绝启动"
  assert_report PHASE preflight "失败阶段为 preflight"
}

s8_other_worktree() {
  title "s8 目标分支被另一个 worktree 检出：停止"
  make_sandbox s8
  if git -C "$WORK" worktree add -q --force "$D/wt2" main 2>/dev/null; then
    run_script
    assert_report RESULT FAILED "拒绝启动"
    assert_report PHASE preflight "失败阶段为 preflight"
  else
    skip "当前 Git 不允许构造双 worktree 检出同一分支"
  fi
}

# ---------------------------------------------------------------- 场景 9：文本冲突 + resume
s9_text_conflict_and_resume() {
  title "s9 文本冲突：保留现场，语义合并后 --resume 继续"
  make_sandbox s9
  upstream_commit "upstream: 修改共享行" shared.txt 'upstream line\n'
  local_commit "local: 修改同一行" shared.txt 'local line\n' >/dev/null
  local before_origin
  before_origin="$(origin_oid)"
  run_script --publish=never
  assert_report RESULT CONFLICT "返回 CONFLICT"
  assert_report PHASE rebase "阶段为 rebase"
  assert_eq "$RC" "20" "退出码为 20"
  if grep -Fq "shared.txt" "$OUT"; then ok "报告列出冲突文件"; else bad "未列出冲突文件"; fi
  if [ -d "$WORK/.git/rebase-merge" ] || [ -d "$WORK/.git/rebase-apply" ]; then
    ok "rebase 现场保留"
  else
    bad "rebase 现场被清理"
  fi
  if [ -n "$(git -C "$WORK" ls-files -u)" ]; then ok "未解决冲突仍在索引中"; else bad "冲突被自动解决了"; fi
  assert_eq "$(origin_oid)" "$before_origin" "冲突期间未推送"
  # 模拟 Agent 语义合并：保留双方有效内容，而不是整文件选 --ours/--theirs。
  printf 'upstream line\nlocal line\n' > "$WORK/shared.txt"
  git -C "$WORK" add shared.txt
  run_script --resume --publish=never
  assert_report RESULT SUCCESS "--resume 成功"
  assert_file_has "$WORK/shared.txt" "upstream line" "上游改动保留"
  assert_file_has "$WORK/shared.txt" "local line" "本地独有改动保留"
}

# ---------------------------------------------------------------- 场景 10：二进制冲突
s10_binary_conflict() {
  title "s10 二进制冲突：证据不足时停止并交人工"
  make_sandbox s10
  printf '\x00\x01UPSTREAM' > "$UP/blob.bin"
  git -C "$UP" add -A
  git -C "$UP" commit -qm "upstream: change blob"
  git -C "$UP" push -q origin main
  printf '\x00\x01LOCAL' > "$WORK/blob.bin"
  git -C "$WORK" add -A
  git -C "$WORK" commit -qm "local: change blob"
  run_script --publish=never
  assert_report RESULT CONFLICT "二进制冲突返回 CONFLICT"
  if grep -Fq "blob.bin" "$OUT"; then ok "报告列出二进制冲突文件"; else bad "未列出二进制冲突文件"; fi
  assert_out_has "未知二进制" "NEXT_STEP 明确要求人工决策"
}

# ---------------------------------------------------------------- 场景 11：advise 发布模式
s11_advise_publish() {
  title "s11 advise 模式：只给命令，不推送"
  make_sandbox s11
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  local before
  before="$(origin_oid)"
  run_script --publish=advise
  assert_report RESULT SUCCESS "重放成功"
  assert_report PUBLISH_RESULT advised "仅给出发布建议"
  assert_out_has "--force-with-lease" "建议命令使用带租约强推"
  assert_eq "$(origin_oid)" "$before" "origin/main 未变化"
}

# ---------------------------------------------------------------- 场景 12：已授权发布
s12_authorized_publish() {
  title "s12 已授权且验证通过：只向核验过的个人 fork 带租约推送"
  make_sandbox s12
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  run_script --publish=auto-lease
  assert_report RESULT SUCCESS "流程成功"
  assert_report PUBLISH_RESULT pushed "已推送"
  assert_eq "$(origin_oid)" "$(work_head)" "origin/main 与本地 HEAD 一致"
  if grep -Fq 'git push --force-with-lease=' "$TARGET"; then
    ok "脚本使用显式租约的 --force-with-lease"
  else
    bad "脚本未使用显式租约"
  fi
  if grep -Fq 'origin "HEAD:refs/heads/$TARGET_BRANCH"' "$TARGET"; then
    ok "推送目标固定为 origin 的固定分支"
  else
    bad "推送目标未固定"
  fi
}

# ---------------------------------------------------------------- 场景 13：发布失败不退回普通 force
s13_publish_failure_no_force_retry() {
  title "s13 发布被拒绝：保留本地结果，不重试普通 --force"
  make_sandbox s13
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  mkdir -p "$D/hooks"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'printf "x" >> %q\n' "$D/push_count"
    printf 'exit 1\n'
  } > "$D/hooks/pre-receive"
  chmod +x "$D/hooks/pre-receive"
  git -C "$D/origin.git" config core.hooksPath "$D/hooks"
  run_script --publish=auto-lease
  assert_report RESULT FAILED "发布失败返回 FAILED"
  assert_report PHASE publish "失败阶段为 publish"
  assert_report PUBLISH_RESULT failed "记录发布失败"
  assert_out_has "绝不退回普通 --force" "明确禁止退回普通强推"
  local count
  count="$(wc -c < "$D/push_count" 2>/dev/null | tr -d ' ')"
  assert_eq "${count:-0}" "1" "仅尝试一次推送，没有普通 --force 重试"
  assert_ne "$(work_head)" "$(git -C "$D/seed" rev-parse HEAD)" "本地 rebase 结果保留"
}

# ---------------------------------------------------------------- 场景 14：验证失败禁止发布
s14_validation_failure_blocks_publish() {
  title "s14 项目验证失败：禁止发布"
  make_sandbox s14
  patch_config PROJECT_TEST_CMD "false"
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  local before
  before="$(origin_oid)"
  run_script --publish=auto-lease
  assert_report RESULT FAILED "验证失败返回 FAILED"
  assert_report PHASE verify "失败阶段为 verify"
  assert_report PUBLISH_RESULT not-attempted "未尝试发布"
  assert_out_has "禁止发布" "明确禁止发布"
  assert_eq "$(origin_oid)" "$before" "origin/main 未变化"
}

# ---------------------------------------------------------------- 场景 15/16/17：智能模式排除提交
s15_smart_exclude_verified() {
  title "s15 智能模式：精确排除一条完整等价提交，不误删其他本地提交"
  make_sandbox s15
  upstream_commit "upstream: 提供 feature 开关" feature.txt 'FEATURE_ENABLED=1\n'
  local dup_hash subjects
  dup_hash="$(local_commit "local: add feature" feature.txt 'FEATURE_ENABLED=1\n# local note\n')"
  local_commit "local: extra" extra.txt 'EXTRA\n' >/dev/null
  printf 'COMPLETE_EQUIVALENCE %s 上游 feature.txt 已提供相同开关行为\n' "$dup_hash" > "$D/evidence.txt"
  run_script --publish=never --exclude "$dup_hash" --evidence "$D/evidence.txt"
  assert_report RESULT SUCCESS "重放成功"
  assert_out_has "$dup_hash" "报告记录被排除提交"
  # 不用 `git log | grep -q`：grep 提前退出会让上游收到 SIGPIPE，pipefail 下会误判。
  subjects="$(git -C "$WORK" log --format='%s')"
  if grep -Fqx -- "local: add feature" <<< "$subjects"; then
    bad "被排除提交仍在新历史中"
  else
    ok "被排除提交已不在历史中"
  fi
  if grep -Fqx -- "local: extra" <<< "$subjects"; then
    ok "其他本地独有提交保留"
  else
    bad "其他本地独有提交丢失"
  fi
  assert_file_has "$WORK/feature.txt" "FEATURE_ENABLED=1" "上游等价功能保留"
  assert_file_has "$WORK/extra.txt" "EXTRA" "本地独有文件保留"
}

s16_partial_coverage_no_exclude() {
  title "s16 仅部分覆盖：禁止排除，停止审查"
  make_sandbox s16
  upstream_commit "upstream: 部分实现 feature" feature.txt 'FEATURE_ENABLED=1\n'
  local dup_hash
  dup_hash="$(local_commit "local: full feature" feature.txt 'FEATURE_ENABLED=1\nEXTRA_BEHAVIOR=on\n')"
  printf 'PARTIAL_COVERAGE %s 上游只覆盖开关，未覆盖 EXTRA_BEHAVIOR\n' "$dup_hash" > "$D/evidence.txt"
  local before
  before="$(work_head)"
  run_script --publish=never --exclude "$dup_hash" --evidence "$D/evidence.txt"
  assert_report RESULT FAILED "拒绝排除"
  assert_eq "$(work_head)" "$before" "HEAD 未改变，没有静默丢提交"
}

s17_no_evidence_no_exclude() {
  title "s17 无证据卡：禁止排除，保留全部提交"
  make_sandbox s17
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local dup_hash
  dup_hash="$(local_commit "local: 本地文件" local_only.txt 'local\n')"
  local before
  before="$(work_head)"
  run_script --publish=never --exclude "$dup_hash"
  assert_report RESULT FAILED "拒绝排除"
  assert_eq "$(work_head)" "$before" "HEAD 未改变"
  assert_out_has "证据卡" "说明缺少证据卡"
}

# ---------------------------------------------------------------- 场景 18：stash 引用移位
s18_stash_reference_changed() {
  title "s18 stash 引用移位：不盲目 pop，标记 reference-changed"
  make_sandbox s18
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  printf 'untracked\n' > "$WORK/new.txt"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'cd %q || exit 1\n' "$WORK"
    printf 'printf "inject\\n" > injected.txt\n'
    printf 'git add injected.txt\n'
    printf 'git stash push -q -m injected\n'
  } > "$D/hook.sh"
  patch_config TEST_POST_REBASE_HOOK "bash $D/hook.sh"
  run_script --publish=never
  assert_report RESULT FAILED "检测到引用移位并停止"
  assert_report STASH_STATE reference-changed "标记 reference-changed"
  assert_report RESTORE_STASH_REQUIRED manual "要求人工恢复"
  assert_out_has "git stash apply" "给出按稳定哈希恢复的指引"
  assert_eq "$(git -C "$WORK" stash list | wc -l | tr -d ' ')" "2" "两个 stash 均保留"
}

# ---------------------------------------------------------------- 场景 19：意外 shell 失败
s19_unexpected_shell_failure() {
  title "s19 意外 shell 失败：输出结构化失败报告，不谎报成功"
  make_sandbox s19
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  patch_config TEST_POST_REBASE_HOOK "false"
  run_script --publish=never
  assert_report RESULT FAILED "报告 FAILED"
  if grep -Eq '^PHASE=[a-z-]+$' "$OUT"; then ok "报告包含 PHASE"; else bad "报告缺少 PHASE"; fi
  if grep -Eq '^STASH_STATE=[a-z-]+$' "$OUT"; then ok "报告包含 STASH_STATE"; else bad "报告缺少 STASH_STATE"; fi
  if [ "$RC" -ne 0 ]; then ok "退出码非零"; else bad "退出码不应为 0"; fi
}

# ---------------------------------------------------------------- 场景 20：origin 领先/分叉不误报
s20_origin_out_of_sync() {
  title "s20 origin 与本地不一致：不误报「已同步无事可做」"
  make_sandbox s20
  printf 'other change\n' > "$D/seed/from_another_clone.txt"
  git -C "$D/seed" add -A
  git -C "$D/seed" commit -qm "origin: 另一处推送"
  git -C "$D/seed" push -q origin main
  local before
  before="$(origin_oid)"
  run_script --check
  assert_report RESULT SUCCESS "--check 成功"
  assert_report UP_TO_DATE yes "本地已包含上游"
  assert_report ORIGIN_STATE out-of-sync "正确报告 origin 不同步"
  assert_out_has "人工核对" "给出人工核对指引"
  assert_eq "$(origin_oid)" "$before" "未自动推送"
}

# ---------------------------------------------------------------- 场景 21：不安装定时任务、不改配置
s21_no_schedule_no_config() {
  title "s21 未授权定时与持久配置：脚本不含调度安装、不改配置"
  make_sandbox s21
  local cfg_before
  cfg_before="$(git -C "$WORK" config --local --list | sort)"
  run_script --check
  assert_eq "$(git -C "$WORK" config --local --list | sort)" "$cfg_before" "--check 不改配置"
  if grep -Eq 'crontab|launchctl|systemctl|schedule' "$TARGET"; then
    bad "脚本含调度器安装逻辑"
  else
    ok "脚本不含任何调度器安装逻辑"
  fi
  if grep -Eq 'git config (--local|--global)|git config [a-z]' "$TARGET"; then
    bad "脚本含持久配置写入"
  else
    ok "脚本不写持久 Git 配置"
  fi
}

# ---------------------------------------------------------------- 场景 22/23：重入保护
s22_concurrent_run_busy() {
  title "s22 已有运行持锁：报告忙碌，不重入、不动 HEAD"
  make_sandbox s22
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  local before
  before="$(work_head)"
  mkdir -p "$WORK/.git/rebase-upstream.lock"
  printf 'pid=%s\nstarted=2026-11-13T00:00:00Z\nmode=run\n' "$$" > "$WORK/.git/rebase-upstream.lock/owner"
  run_script --publish=never
  assert_report RESULT FAILED "拒绝重入"
  assert_report BUSY yes "标记 BUSY"
  assert_eq "$RC" "22" "退出码为 22"
  assert_eq "$(work_head)" "$before" "HEAD 未改变"
  if [ -d "$WORK/.git/rebase-upstream.lock" ]; then ok "未删除他人的锁"; else bad "误删了他人的锁"; fi
}

s23_stale_lock_recovered() {
  title "s23 陈旧锁：自动清理后正常执行且释放锁"
  make_sandbox s23
  upstream_commit "upstream: 新增文件" upstream_only.txt 'upstream\n'
  local_commit "local: 本地文件" local_only.txt 'local\n' >/dev/null
  mkdir -p "$WORK/.git/rebase-upstream.lock"
  printf 'pid=999999\nstarted=2026-11-13T00:00:00Z\nmode=run\n' > "$WORK/.git/rebase-upstream.lock/owner"
  run_script --publish=never
  assert_report RESULT SUCCESS "清理陈旧锁后成功"
  assert_out_has "陈旧锁" "记录清理动作"
  if [ -e "$WORK/.git/rebase-upstream.lock" ]; then bad "运行结束后锁未释放"; else ok "运行结束后锁已释放"; fi
}

main() {
  printf '隔离回归测试：%s\n' "$ROOT"
  printf '被测脚本：%s\n' "$TARGET"
  s1_no_upstream_change
  s2_rebase_local_commit
  s3_stash_including_untracked
  s4_fetch_failure
  s5_wrong_origin_url
  s6_in_progress_operation
  s7_detached_head
  s8_other_worktree
  s9_text_conflict_and_resume
  s10_binary_conflict
  s11_advise_publish
  s12_authorized_publish
  s13_publish_failure_no_force_retry
  s14_validation_failure_blocks_publish
  s15_smart_exclude_verified
  s16_partial_coverage_no_exclude
  s17_no_evidence_no_exclude
  s18_stash_reference_changed
  s19_unexpected_shell_failure
  s20_origin_out_of_sync
  s21_no_schedule_no_config
  s22_concurrent_run_busy
  s23_stale_lock_recovered
  printf '\n合计：通过 %s，失败 %s，跳过 %s\n' "$PASS" "$FAIL" "$SKIP"
  if [ "$FAIL" -gt 0 ]; then exit 1; fi
}

main "$@"
