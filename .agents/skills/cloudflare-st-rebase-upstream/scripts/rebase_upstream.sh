#!/usr/bin/env bash
# CloudflareST 项目专用：把本地 main 的独有提交重放到已核验的 masgzy/CloudflareST main 之上。
#
# 本脚本只做确定性的 Git 流程，不理解业务语义：
#   - 「完整等价提交」的判定证据卡由调用本技能的 Agent 生成，脚本只校验并执行精确排除；
#   - 语义冲突合并由 Agent 读取三方版本完成，脚本只保留现场并提供 --resume 继续入口。
#
# 生成时的策略见 ../references/rebase-policy.md。本文件不依赖任何客户端或用户主目录资源。
set -Eeuo pipefail

# ---------------------------------------------------------------- 项目身份与策略常量
# 生产默认。仅 --test-config 可覆盖；隔离回归测试专用，生产调用不得带该参数。
REPO_SLUG="cloudflare-st"
REPO_MARKER_FILE="go.mod"
REPO_MARKER_PATTERN='^module github\.com/masgzy/CloudflareST$'
APPROVED_ORIGIN_URL="https://github.com/lazzman/CloudflareST.git"
UPSTREAM_URL="https://github.com/masgzy/CloudflareST.git"
UPSTREAM_BRANCH="main"
TARGET_BRANCH="main"
TRACKING_REF="refs/remotes/rebase-upstream/main"
PROJECT_TEST_CMD="go test ./..."
PUBLISH_AUTHORIZED="auto-lease"
PUBLISH_TARGET_CONFIRMED_EXCLUSIVE="yes"
DUPLICATE_POLICY="drop-verified"
CONFLICT_POLICY="semantic-verified"
TEST_PRE_PUBLISH_HOOK=""
TEST_POST_REBASE_HOOK=""

# ---------------------------------------------------------------- 运行时状态（全部先初始化，避免未知状态被当成成功）
MODE="run"
PUBLISH_MODE=""
TEST_MODE="0"
CONFIG_FILE=""
EVIDENCE_FILE=""
EXCLUDE_HASHES=""
EXCLUDE_COUNT=0
PHASE="preflight"
RESULT="FAILED"
BRANCH=""
ORIGINAL_HEAD=""
REBASED_HEAD=""
UPSTREAM_COMMIT=""
MERGE_BASE=""
STASH_REF=""
STASH_COMMIT=""
STASH_STATE="none"
RESTORE_STASH_REQUIRED="no"
CONFLICT_FILES=""
REBASE_HEAD_HASH=""
PUBLISH_RESULT="not-attempted"
ORIGIN_STATE="unknown"
UP_TO_DATE="no"
NEXT_STEP=""
EXTRA_NOTES=""
FAIL_REASON=""
STATE_FILE=""
GIT_DIR_ABS=""
REPO_TOP=""
LOCK_DIR=""
LOCK_HELD="no"
BUSY="no"

log() { printf '[%s-rebase-upstream] %s\n' "$REPO_SLUG" "$*" >&2; }

normalize_url() {
  # 接受同一仓库的等价 Git URL 表达，但绝不把不同仓库判成相同。
  local u="$1"
  case "$u" in
    /*) printf '%s' "${u%/}" ; return ;;
    file://*) u="${u#file://}" ; printf '%s' "${u%/}" ; return ;;
  esac
  u="${u#*://}"          # 去协议
  u="${u#*@}"            # 去认证信息
  printf '%s' "$u" | sed -E 's#^([^/]+):#\1/#; s#\.git$##; s#/$##'
}

emit_report() {
  printf 'RESULT=%s\n' "$RESULT"
  printf 'PHASE=%s\n' "$PHASE"
  printf 'MODE=%s\n' "$MODE"
  printf 'TEST_MODE=%s\n' "$TEST_MODE"
  printf 'BRANCH=%s\n' "$BRANCH"
  printf 'UPSTREAM=%s %s\n' "$UPSTREAM_URL" "$UPSTREAM_BRANCH"
  printf 'UPSTREAM_COMMIT=%s\n' "$UPSTREAM_COMMIT"
  printf 'ORIGINAL_HEAD=%s\n' "$ORIGINAL_HEAD"
  printf 'REBASED_HEAD=%s\n' "$REBASED_HEAD"
  printf 'STASH_REF=%s\n' "$STASH_REF"
  printf 'STASH_COMMIT=%s\n' "$STASH_COMMIT"
  printf 'STASH_STATE=%s\n' "$STASH_STATE"
  printf 'RESTORE_STASH_REQUIRED=%s\n' "$RESTORE_STASH_REQUIRED"
  printf 'EXCLUDED_COMMITS=%s\n' "$EXCLUDE_HASHES"
  printf 'CONFLICT_FILES=%s\n' "$CONFLICT_FILES"
  printf 'REBASE_HEAD=%s\n' "$REBASE_HEAD_HASH"
  printf 'PUBLISH_MODE=%s\n' "$PUBLISH_MODE"
  printf 'PUBLISH_RESULT=%s\n' "$PUBLISH_RESULT"
  printf 'ORIGIN_STATE=%s\n' "$ORIGIN_STATE"
  printf 'UP_TO_DATE=%s\n' "$UP_TO_DATE"
  printf 'STATE_FILE=%s\n' "$STATE_FILE"
  printf 'NEXT_STEP=%s\n' "$NEXT_STEP"
  printf 'NOTES=%s\n' "$EXTRA_NOTES"
  printf 'FAIL_REASON=%s\n' "$FAIL_REASON"
  printf 'BUSY=%s\n' "$BUSY"
}

on_exit() {
  local rc=$?          # 必须在 set +e 之前取，否则退出码会被重置为 0
  set +e
  trap - EXIT ERR
  release_lock
  if [ "$RESULT" = "FAILED" ] && [ -z "$NEXT_STEP" ]; then
    NEXT_STEP="检查上方日志与 ORIGINAL_HEAD；本轮未改写远程，必要时从 ORIGINAL_HEAD 新建恢复分支"
  fi
  emit_report
  exit "$rc"
}
trap on_exit EXIT

on_err() {
  local rc=$? line=$1
  log "意外 shell 失败（第 ${line} 行，退出码 ${rc}）"
  RESULT="FAILED"
  exit "$rc"
}
trap 'on_err $LINENO' ERR

finish() { exit "$1"; }

fail() {
  log "ERROR: $*"
  RESULT="FAILED"
  FAIL_REASON="$*"
  if [ -z "$NEXT_STEP" ]; then
    NEXT_STEP="检查上方日志；若 HEAD 已改变，可用 git switch -c recovery/<日期> ${ORIGINAL_HEAD:-HEAD} 取回原始历史"
  fi
  finish 1
}

usage() {
  cat <<'EOF'
用法: rebase_upstream.sh [选项]

  (无参数)              执行完整流程：preflight → fetch → stash → rebase → 验证 → 发布
  --check               只读检查并抓取上游，报告是否有增量；不改工作区、不推送
  --resume              冲突已由 Agent 语义合并并 git add 后，继续 rebase 及后续阶段
  --publish=MODE        never | advise | auto-lease（默认取策略授权值）
  --exclude HASH        智能模式：排除一条经证据卡核验为「完整等价」的本地提交，可重复
  --evidence FILE       证据卡文件（每行: COMPLETE_EQUIVALENCE <hash> <理由>）
  --test-config FILE    仅隔离回归测试：覆盖项目身份/远程/测试命令等常量
  -h | --help           显示本说明

退出码: 0 成功/无更新, 20 冲突待人工或 Agent 处理, 21 stash 恢复冲突, 1 其他失败。
EOF
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --check) MODE="check" ;;
      --resume) MODE="resume" ;;
      --publish=*) PUBLISH_MODE="${1#--publish=}" ;;
      --publish) shift; [ "$#" -gt 0 ] || fail "--publish 缺少模式"; PUBLISH_MODE="$1" ;;
      --exclude)
        shift
        [ "$#" -gt 0 ] || fail "--exclude 缺少提交哈希"
        EXCLUDE_HASHES="${EXCLUDE_HASHES} $1"
        EXCLUDE_COUNT=$((EXCLUDE_COUNT + 1))
        ;;
      --evidence) shift; [ "$#" -gt 0 ] || fail "--evidence 缺少文件"; EVIDENCE_FILE="$1" ;;
      --test-config)
        shift
        [ "$#" -gt 0 ] || fail "--test-config 缺少文件"
        CONFIG_FILE="$1"
        TEST_MODE="1"
        # shellcheck disable=SC1090
        source "$CONFIG_FILE"
        log "TEST MODE：已从 $CONFIG_FILE 覆盖项目身份常量；生产调用不得使用此参数"
        ;;
      -h|--help) usage; RESULT="SUCCESS"; PHASE="complete"; finish 0 ;;
      *) fail "未知参数: $1" ;;
    esac
    shift
  done
  case "$PUBLISH_MODE" in
    "") PUBLISH_MODE="$PUBLISH_AUTHORIZED" ;;
    never|advise|auto-lease) ;;
    *) fail "不支持的 --publish 模式: $PUBLISH_MODE" ;;
  esac
  if [ "$MODE" = "check" ] && [ "$EXCLUDE_COUNT" -gt 0 ]; then
    fail "--check 不接受 --exclude"
  fi
}

# ---------------------------------------------------------------- 状态持久化（供 --resume / 人工恢复）
save_state() {
  [ -n "$STATE_FILE" ] || return 0
  {
    printf 'ORIGINAL_HEAD=%q\n' "$ORIGINAL_HEAD"
    printf 'UPSTREAM_COMMIT=%q\n' "$UPSTREAM_COMMIT"
    printf 'MERGE_BASE=%q\n' "$MERGE_BASE"
    printf 'STASH_REF=%q\n' "$STASH_REF"
    printf 'STASH_COMMIT=%q\n' "$STASH_COMMIT"
    printf 'STASH_STATE=%q\n' "$STASH_STATE"
    printf 'EXCLUDE_HASHES=%q\n' "$EXCLUDE_HASHES"
    printf 'EXCLUDE_COUNT=%q\n' "$EXCLUDE_COUNT"
    printf 'PUBLISH_MODE=%q\n' "$PUBLISH_MODE"
    printf 'CONFIG_FILE=%q\n' "$CONFIG_FILE"
    printf 'TEST_MODE=%q\n' "$TEST_MODE"
    printf 'PHASE=%q\n' "$PHASE"
  } > "$STATE_FILE"
}

load_state() {
  [ -f "$STATE_FILE" ] || fail "没有可恢复的状态文件: $STATE_FILE"
  # shellcheck disable=SC1090
  source "$STATE_FILE"
  if [ -n "$CONFIG_FILE" ]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
  fi
}

# ---------------------------------------------------------------- 重入保护（同一仓库同时只允许一个运行）
release_lock() {
  if [ "$LOCK_HELD" != "yes" ]; then return 0; fi
  if [ -f "$LOCK_DIR/owner" ] && grep -qx "pid=$$" "$LOCK_DIR/owner"; then
    rm -rf "$LOCK_DIR"
  fi
  LOCK_HELD="no"
}

acquire_lock() {
  GIT_DIR_ABS="$(git rev-parse --absolute-git-dir 2>/dev/null || true)"
  [ -n "$GIT_DIR_ABS" ] || fail "无法定位 .git 目录"
  REPO_TOP="$(git rev-parse --show-toplevel 2>/dev/null || true)"
  STATE_FILE="$GIT_DIR_ABS/rebase-upstream-state.env"
  LOCK_DIR="$GIT_DIR_ABS/rebase-upstream.lock"
  if mkdir "$LOCK_DIR" 2>/dev/null; then
    LOCK_HELD="yes"
    printf 'pid=%s\nstarted=%s\nmode=%s\n' "$$" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$MODE" > "$LOCK_DIR/owner"
    return 0
  fi
  local pid=""
  if [ -f "$LOCK_DIR/owner" ]; then
    pid="$(sed -n 's/^pid=//p' "$LOCK_DIR/owner" | head -1)"
  fi
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    BUSY="yes"
    RESULT="FAILED"
    FAIL_REASON="另一个 rebase-upstream 正在运行（pid $pid）；本轮不重入"
    NEXT_STEP="等待上一个运行结束；若确认进程已退出，人工删除 $LOCK_DIR 后再运行"
    finish 22
  fi
  log "清理陈旧锁（pid=${pid:-未知} 已不存在）：$LOCK_DIR"
  EXTRA_NOTES="已清理陈旧锁（pid=${pid:-未知}）；如非预期请检查是否有异常中断的运行"
  rm -rf "$LOCK_DIR"
  if mkdir "$LOCK_DIR" 2>/dev/null; then
    LOCK_HELD="yes"
    printf 'pid=%s\nstarted=%s\nmode=%s\n' "$$" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$MODE" > "$LOCK_DIR/owner"
  else
    BUSY="yes"
    RESULT="FAILED"
    FAIL_REASON="无法获取运行锁：$LOCK_DIR"
    finish 22
  fi
}

# ---------------------------------------------------------------- 前置校验
preflight() {
  PHASE="preflight"
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "当前目录不是 Git 工作树"
  REPO_TOP="$(git rev-parse --show-toplevel)"
  GIT_DIR_ABS="$(git rev-parse --absolute-git-dir)"
  STATE_FILE="$GIT_DIR_ABS/rebase-upstream-state.env"

  local origin_url
  origin_url="$(git remote get-url origin 2>/dev/null || true)"
  [ -n "$origin_url" ] || fail "缺少 origin 远程"
  if [ "$(normalize_url "$origin_url")" != "$(normalize_url "$APPROVED_ORIGIN_URL")" ]; then
    fail "origin URL 与批准值不符：实际=$origin_url 批准=$APPROVED_ORIGIN_URL"
  fi
  [ -f "$REPO_TOP/$REPO_MARKER_FILE" ] || fail "缺少项目标识文件 $REPO_MARKER_FILE"
  grep -Eq "$REPO_MARKER_PATTERN" "$REPO_TOP/$REPO_MARKER_FILE" \
    || fail "项目标识不匹配：不是 $REPO_SLUG 仓库"

  BRANCH="$(git symbolic-ref --quiet --short HEAD || true)"
  [ -n "$BRANCH" ] || fail "detached HEAD，拒绝执行"
  [ "$BRANCH" = "$TARGET_BRANCH" ] \
    || fail "当前分支 $BRANCH 不是目标分支 $TARGET_BRANCH；不会切换你正在编辑的分支"

  for marker in rebase-merge rebase-apply MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD; do
    if [ -e "$GIT_DIR_ABS/$marker" ]; then
      fail "检测到进行中的 Git 操作（$marker），请先完成或中止"
    fi
  done

  # 不扰动其他 worktree：目标分支若被别的 worktree 检出，直接停止。
  local wt_branch wt_path other="" canonical_top canonical_wt
  canonical_top="$(cd "$REPO_TOP" && pwd -P)"
  while IFS= read -r line; do
    case "$line" in
      "worktree "*) wt_path="${line#worktree }" ;;
      "branch "*) wt_branch="${line#branch }"
        canonical_wt="$(cd "$wt_path" 2>/dev/null && pwd -P || printf '%s' "$wt_path")"
        if [ "$wt_branch" = "refs/heads/$TARGET_BRANCH" ] && [ "$canonical_wt" != "$canonical_top" ]; then
          other="$wt_path"
        fi ;;
    esac
  done < <(git worktree list --porcelain)
  [ -z "$other" ] || fail "目标分支 $TARGET_BRANCH 也被 worktree 检出：$other；不擅自切换或清理"

  ORIGINAL_HEAD="$(git rev-parse HEAD)"
  log "项目与分支校验通过：$REPO_TOP @ $BRANCH ($ORIGINAL_HEAD)"
}

# ---------------------------------------------------------------- 抓取并验证上游
fetch_and_verify() {
  PHASE="fetch"
  # 使用已核验 URL + 精确 refspec，不写 remote.* 配置，不触碰 origin 引用。
  if ! git fetch --no-tags --prune "$UPSTREAM_URL" "+refs/heads/$UPSTREAM_BRANCH:$TRACKING_REF"; then
    fail "上游抓取失败；停止，绝不回退到过期引用，也不会先 stash"
  fi
  PHASE="verify-upstream"
  UPSTREAM_COMMIT="$(git rev-parse --verify "$TRACKING_REF^{commit}" 2>/dev/null || true)"
  [ -n "$UPSTREAM_COMMIT" ] || fail "上游引用 $TRACKING_REF 无法解析为提交"
  MERGE_BASE="$(git merge-base HEAD "$UPSTREAM_COMMIT" 2>/dev/null || true)"
  [ -n "$MERGE_BASE" ] || fail "本地 HEAD 与上游没有共同祖先，需人工审查，不能盲目 rebase"
  log "上游基线=$UPSTREAM_COMMIT 共同祖先=$MERGE_BASE"

  # origin 同步状态：只有远程 ref 确实等于本地 HEAD 才算已同步。
  local remote_oid
  remote_oid="$(git ls-remote --heads origin "refs/heads/$TARGET_BRANCH" 2>/dev/null | awk 'NR==1{print $1}' || true)"
  if [ -z "$remote_oid" ]; then
    ORIGIN_STATE="missing"
  elif [ "$remote_oid" = "$ORIGINAL_HEAD" ]; then
    ORIGIN_STATE="synced"
  else
    ORIGIN_STATE="out-of-sync"
  fi

  if git merge-base --is-ancestor "$UPSTREAM_COMMIT" HEAD; then
    UP_TO_DATE="yes"
    REBASED_HEAD="$ORIGINAL_HEAD"
    PHASE="complete"
    RESULT="SUCCESS"
    if [ "$ORIGIN_STATE" != "synced" ]; then
      EXTRA_NOTES="本地已包含上游，但 origin/$TARGET_BRANCH 与本地不一致（$ORIGIN_STATE）；本轮不自动发布，需人工核对"
      NEXT_STEP="核对 origin/$TARGET_BRANCH 与本地提交关系后再决定是否发布"
    else
      NEXT_STEP="无需操作：本地已包含上游且 origin 已同步"
    fi
    log "已是最新，未改写 HEAD、未创建 stash、未推送"
    return 1   # 告知调用方走「无更新」分支
  fi
  return 0
}

# ---------------------------------------------------------------- 未提交/未跟踪文件保护
protect_worktree() {
  PHASE="stash"
  if [ -n "$(git status --porcelain=v1)" ]; then
    # 注意：必须在 fetch 成功之后才 stash，避免上游不可用时先动工作区。
    git stash push --include-untracked -m "rebase-upstream auto stash" >/dev/null
    STASH_REF="stash@{0}"
    STASH_COMMIT="$(git rev-parse --verify "$STASH_REF")"
    STASH_STATE="pending"
    RESTORE_STASH_REQUIRED="yes"
    log "已保护未提交与未跟踪文件：$STASH_REF -> $STASH_COMMIT"
  else
    STASH_STATE="none"
  fi
  save_state
}

restore_stash() {
  [ "$STASH_STATE" = "pending" ] || return 0
  PHASE="restore-stash"
  local current
  current="$(git rev-parse --verify 'stash@{0}' 2>/dev/null || true)"
  if [ "$current" != "$STASH_COMMIT" ]; then
    STASH_STATE="reference-changed"
    RESTORE_STASH_REQUIRED="manual"
    RESULT="FAILED"
    NEXT_STEP="按稳定哈希 $STASH_COMMIT 人工定位并恢复 stash：git stash list / git stash apply $STASH_COMMIT；不要盲目 pop"
    fail "stash 引用已移位（期望 $STASH_COMMIT，实际 ${current:-无}），停止自动恢复"
  fi
  if ! git stash pop 'stash@{0}'; then
    STASH_STATE="conflict"
    RESTORE_STASH_REQUIRED="manual"
    RESULT="STASH_CONFLICT"
    CONFLICT_FILES="$(git diff --name-only --diff-filter=U | paste -sd, -)"
    PHASE="restore-stash"
    NEXT_STEP="保留 stash $STASH_COMMIT 与三方冲突现场；人工解决后 git add 并 git stash drop，勿覆盖用户改动"
    finish 21
  fi
  STASH_STATE="restored"
  RESTORE_STASH_REQUIRED="no"
  save_state
}

# ---------------------------------------------------------------- 智能模式：完整等价提交的精确排除
is_excluded() {
  local needle="$1" e
  for e in $EXCLUDE_HASHES; do
    if [ "$e" = "$needle" ]; then return 0; fi
  done
  return 1
}

evidence_says_complete() {
  local commit="$1" tag h full found="no"
  [ -f "$EVIDENCE_FILE" ] || return 1
  while read -r tag h _; do
    [ -n "$tag" ] || continue
    case "$tag" in
      PARTIAL_COVERAGE|INSUFFICIENT_EVIDENCE|DEPENDENT_COMMIT)
        full="$(git rev-parse --verify "$h^{commit}" 2>/dev/null || true)"
        if [ "$full" = "$commit" ]; then return 1; fi ;;
      COMPLETE_EQUIVALENCE)
        full="$(git rev-parse --verify "$h^{commit}" 2>/dev/null || true)"
        if [ "$full" = "$commit" ]; then found="yes"; fi ;;
    esac
  done < "$EVIDENCE_FILE"
  [ "$found" = "yes" ]
}

validate_exclusions() {
  [ "$EXCLUDE_COUNT" -gt 0 ] || return 0
  [ "$DUPLICATE_POLICY" = "drop-verified" ] || fail "当前策略不允许排除提交（$DUPLICATE_POLICY）"
  [ -n "$EVIDENCE_FILE" ] || fail "排除提交必须提供 --evidence 证据卡；无证据时一律保留全部提交"
  [ -f "$EVIDENCE_FILE" ] || fail "证据卡不存在: $EVIDENCE_FILE"

  local c full parents replay
  replay="$(git rev-list --reverse "$MERGE_BASE..$ORIGINAL_HEAD")"
  for c in $EXCLUDE_HASHES; do
    full="$(git rev-parse --verify "$c^{commit}" 2>/dev/null || true)"
    [ -n "$full" ] || fail "无法解析排除提交 $c"
    grep -qx "$full" <<< "$replay" || fail "排除提交 $c 不在待重放范围内"
    parents="$(git rev-list --parents -n 1 "$full" | wc -w | tr -d ' ')"
    [ "$parents" -le 2 ] || fail "排除提交 $full 是 merge 提交，需人工审查"
    evidence_says_complete "$full" || fail "提交 $full 缺少「完整等价」证据或被标记为部分覆盖/证据不足，禁止排除"
    log "已核验完整等价证据：$full"
  done
  # 提交关系复杂（merge、上游历史非线性和）时停止，不用大范围 --onto 冒险。
  for c in $replay; do
    parents="$(git rev-list --parents -n 1 "$c" | wc -w | tr -d ' ')"
    [ "$parents" -le 2 ] || fail "待重放历史含 merge 提交 $c，停止人工审查"
  done
}

make_sequence_editor() {
  local editor
  editor="$(mktemp "${TMPDIR:-/tmp}/rebase-upstream-seq.XXXXXX")"
  cat > "$editor" <<'EDITOR'
#!/usr/bin/env bash
set -euo pipefail
todo="$1"
tmp="$(mktemp "${TMPDIR:-/tmp}/rebase-upstream-todo.XXXXXX")"
count=0
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    "pick "*)
      h="$(printf '%s\n' "$line" | awk '{print $2}')"
      full="$(git rev-parse --verify "$h^{commit}" 2>/dev/null || printf '')"
      for e in ${SEQ_EXCLUDES:-}; do
        if [ "$full" = "$e" ]; then
          line="drop ${line#pick }"
          count=$((count + 1))
          break
        fi
      done
      ;;
  esac
  printf '%s\n' "$line" >> "$tmp"
done < "$todo"
mv "$tmp" "$todo"
printf '%s' "$count" > "${SEQ_COUNT_FILE:?}"
if [ "$count" -ne "${SEQ_EXPECTED:?}" ]; then
  printf 'sequence editor: 预期 %s 条 drop，实际 %s 条\n' "$SEQ_EXPECTED" "$count" >&2
  exit 1
fi
EDITOR
  chmod +x "$editor"
  printf '%s' "$editor"
}

report_conflict() {
  CONFLICT_FILES="$(git diff --name-only --diff-filter=U | paste -sd, -)"
  REBASE_HEAD_HASH="$(git rev-parse --verify REBASE_HEAD 2>/dev/null || true)"
  RESULT="CONFLICT"
  PHASE="rebase"
  if [ "$STASH_STATE" = "pending" ]; then
    RESTORE_STASH_REQUIRED="manual"
  else
    RESTORE_STASH_REQUIRED="no"
  fi
  NEXT_STEP="Agent 逐文件读取三方版本（git show :1:<path> 共同祖先 / :2:<path> 上游基线 / :3:<path> 正在重放的本地提交），语义合并后 git add，再运行本脚本 --resume；互斥实现、未知二进制或验证失败时停止交人工，禁止整文件 --ours/--theirs、禁止 rebase --skip/--abort"
  EXTRA_NOTES="冲突现场已保留，未自动恢复 stash，未推送；rebase 中 --ours 是上游基线、--theirs 是本地提交"
  save_state
  finish 20
}

run_rebase() {
  PHASE="rebase"
  validate_exclusions
  local editor="" count_file=""
  if [ "$EXCLUDE_COUNT" -gt 0 ]; then
    editor="$(make_sequence_editor)"
    count_file="$(mktemp "${TMPDIR:-/tmp}/rebase-upstream-count.XXXXXX")"
    log "智能模式：将精确排除 $EXCLUDE_COUNT 条完整等价提交（受控 todo，非大范围 --onto）"
    if ! env SEQ_EXCLUDES="$EXCLUDE_HASHES" SEQ_EXPECTED="$EXCLUDE_COUNT" \
        SEQ_COUNT_FILE="$count_file" GIT_SEQUENCE_EDITOR="$editor" GIT_EDITOR=true \
        git rebase -i --onto "$UPSTREAM_COMMIT" "$MERGE_BASE" "$TARGET_BRANCH"; then
      rm -f "$editor" "$count_file"
      if [ -d "$GIT_DIR_ABS/rebase-merge" ] || [ -d "$GIT_DIR_ABS/rebase-apply" ]; then
        report_conflict
      fi
      fail "交互式 rebase 未能完成"
    fi
    local dropped
    dropped="$(cat "$count_file")"
    [ "$dropped" -eq "$EXCLUDE_COUNT" ] || fail "排除数量不符（预期 $EXCLUDE_COUNT，实际 $dropped）"
    rm -f "$editor" "$count_file"
  else
    if ! git rebase --onto "$UPSTREAM_COMMIT" "$MERGE_BASE" "$TARGET_BRANCH"; then
      if [ -d "$GIT_DIR_ABS/rebase-merge" ] || [ -d "$GIT_DIR_ABS/rebase-apply" ]; then
        report_conflict
      fi
      fail "rebase 未能完成"
    fi
  fi
  REBASED_HEAD="$(git rev-parse HEAD)"
  save_state
}

verify_exclusions_after_rebase() {
  [ "$EXCLUDE_COUNT" -gt 0 ] || return 0
  local new_subjects c subject
  new_subjects="$(git log --format='%s' "$UPSTREAM_COMMIT..HEAD")"
  for c in $EXCLUDE_HASHES; do
    subject="$(git log -1 --format='%s' "$c")"
    if grep -Fqx "$subject" <<< "$new_subjects"; then
      fail "被授权排除的提交仍在新历史中：$c"
    fi
  done
  local replay
  replay="$(git rev-list --reverse "$MERGE_BASE..$ORIGINAL_HEAD")"
  for c in $replay; do
    if is_excluded "$c"; then continue; fi
    subject="$(git log -1 --format='%s' "$c")"
    grep -Fqx "$subject" <<< "$new_subjects" \
      || fail "独有的本地提交疑似丢失：$c ($subject)"
  done
  log "排除范围校验通过：其余本地独有提交均保留"
}

# ---------------------------------------------------------------- 验证
run_verification() {
  PHASE="verify"
  [ -z "$(git ls-files -u)" ] || fail "仍有未解决的冲突"
  git diff --check || fail "git diff --check 未通过"
  git log --oneline "$UPSTREAM_COMMIT..HEAD" >/dev/null || fail "无法列出本地独有提交"
  git range-diff "$MERGE_BASE..$ORIGINAL_HEAD" "$UPSTREAM_COMMIT..HEAD" >/dev/null 2>&1 \
    || log "git range-diff 不适用或无法比较，已记录"
  git fsck --no-reflogs --no-progress >/dev/null 2>&1 || fail "git fsck 未通过"
  [ "$STASH_STATE" = "restored" ] || [ "$STASH_STATE" = "none" ] \
    || fail "stash 尚未恢复（$STASH_STATE），禁止发布"
  verify_exclusions_after_rebase

  log "运行项目验证：$PROJECT_TEST_CMD"
  if ! ( cd "$REPO_TOP" && eval "$PROJECT_TEST_CMD" ); then
    EXTRA_NOTES="项目验证未通过，已禁止发布；本地 rebase 结果保留"
    fail "项目验证失败：$PROJECT_TEST_CMD"
  fi
}

# ---------------------------------------------------------------- 发布（独立授权 + 固定目标 + 显式租约）
maybe_publish() {
  PHASE="publish"
  case "$PUBLISH_MODE" in
    never)
      PUBLISH_RESULT="disabled"
      NEXT_STEP="仅在本地完成；如需发布，核对后用带租约强推手动发布"
      return 0 ;;
    advise)
      PUBLISH_RESULT="advised"
      EXTRA_NOTES="发布建议：git push --force-with-lease origin HEAD:refs/heads/$TARGET_BRANCH；执行前核验 origin URL、远端引用与租约"
      NEXT_STEP="本地完成；如需发布请按 NOTES 中的建议命令手动执行"
      return 0 ;;
  esac
  [ "$PUBLISH_AUTHORIZED" = "auto-lease" ] || fail "未授权自动发布"
  [ "$PUBLISH_TARGET_CONFIRMED_EXCLUSIVE" = "yes" ] \
    || fail "发布目标未完成「个人独占仓库/分支」确认"
  local origin_url
  origin_url="$(git remote get-url origin)"
  [ "$(normalize_url "$origin_url")" = "$(normalize_url "$APPROVED_ORIGIN_URL")" ] \
    || fail "发布前 origin URL 复核失败，禁止推送"
  # 固定推送目标为 origin 的固定分支；脚本从不把 UPSTREAM_URL 作为 push 目标。
  local remote_oid local_head
  remote_oid="$(git ls-remote --heads origin "refs/heads/$TARGET_BRANCH" | awk 'NR==1{print $1}')"
  [ -n "$remote_oid" ] || fail "目标远程分支不存在，禁止自动发布（避免误建分支）"
  if [ -n "$TEST_PRE_PUBLISH_HOOK" ] && [ "$TEST_MODE" = "1" ]; then
    eval "$TEST_PRE_PUBLISH_HOOK"
  fi
  local_head="$(git rev-parse HEAD)"
  if [ "$remote_oid" = "$local_head" ]; then
    PUBLISH_RESULT="already-synced"
    return 0
  fi
  if ! git push --force-with-lease="refs/heads/$TARGET_BRANCH:$remote_oid" \
        origin "HEAD:refs/heads/$TARGET_BRANCH"; then
    PUBLISH_RESULT="failed"
    EXTRA_NOTES="发布失败：保留本地 rebase 结果，绝不退回普通 --force 或自动重试"
    fail "带租约推送失败（可能是租约冲突或远程拒绝）"
  fi
  PUBLISH_RESULT="pushed"
  ORIGIN_STATE="synced"
}

complete() {
  PHASE="complete"
  RESULT="SUCCESS"
  REBASED_HEAD="$(git rev-parse HEAD)"
  NEXT_STEP="完成；如需回滚，用 git switch -c recovery/<日期> $ORIGINAL_HEAD 从原始历史新建分支（脚本不执行 reset --hard）"
  if [ -z "$EXTRA_NOTES" ]; then
    EXTRA_NOTES="已按策略完成；未修改 Git 持久配置、未创建定时任务"
  fi
  save_state
  rm -f "$STATE_FILE"
}

# ---------------------------------------------------------------- resume 分支
resume_flow() {
  PHASE="rebase"
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "当前目录不是 Git 工作树"
  REPO_TOP="$(git rev-parse --show-toplevel)"
  GIT_DIR_ABS="$(git rev-parse --absolute-git-dir)"
  STATE_FILE="$GIT_DIR_ABS/rebase-upstream-state.env"
  local resume_origin rebase_head_name=""
  resume_origin="$(git remote get-url origin 2>/dev/null || true)"
  [ "$(normalize_url "$resume_origin")" = "$(normalize_url "$APPROVED_ORIGIN_URL")" ] \
    || fail "resume 时 origin URL 与批准值不符，停止"
  load_state
  # rebase 进行中 HEAD 处于 detached 状态，不能用 symbolic-ref 判断分支。
  for d in rebase-merge rebase-apply; do
    if [ -f "$GIT_DIR_ABS/$d/head-name" ]; then
      rebase_head_name="$(cat "$GIT_DIR_ABS/$d/head-name")"
    fi
  done
  BRANCH="$TARGET_BRANCH (resume)"
  if [ -n "$rebase_head_name" ] && [ "$rebase_head_name" != "refs/heads/$TARGET_BRANCH" ]; then
    fail "resume 中的 rebase 目标分支不是 $TARGET_BRANCH（$rebase_head_name）"
  fi
  [ -d "$GIT_DIR_ABS/rebase-merge" ] || [ -d "$GIT_DIR_ABS/rebase-apply" ] \
    || fail "没有进行中的 rebase，无法 --resume"
  [ -z "$(git ls-files -u)" ] || report_conflict
  if ! GIT_EDITOR=true git rebase --continue; then
    if [ -d "$GIT_DIR_ABS/rebase-merge" ] || [ -d "$GIT_DIR_ABS/rebase-apply" ]; then
      report_conflict
    fi
    fail "rebase --continue 未能完成"
  fi
  REBASED_HEAD="$(git rev-parse HEAD)"
  save_state
  restore_stash
  run_verification
  maybe_publish
  complete
}

# ---------------------------------------------------------------- 主流程
main() {
  parse_args "$@"
  acquire_lock
  if [ "$PUBLISH_MODE" = "auto-lease" ] && [ "$TEST_MODE" != "1" ]; then
    log "策略已授权自动发布：验证通过后将带租约推送到固定 origin/$TARGET_BRANCH"
  fi

  case "$MODE" in
    resume)
      resume_flow
      ;;
    check)
      preflight
      if ! fetch_and_verify; then
        RESULT="SUCCESS"; PHASE="complete"; finish 0
      fi
      RESULT="SUCCESS"; PHASE="complete"
      NEXT_STEP="检测到上游增量；正式执行请不带 --check 重新运行"
      finish 0
      ;;
    run)
      preflight
      if ! fetch_and_verify; then
        finish 0
      fi
      protect_worktree
      run_rebase
      if [ -n "$TEST_POST_REBASE_HOOK" ] && [ "$TEST_MODE" = "1" ]; then
        eval "$TEST_POST_REBASE_HOOK"
      fi
      restore_stash
      run_verification
      maybe_publish
      complete
      ;;
  esac
}

main "$@"
