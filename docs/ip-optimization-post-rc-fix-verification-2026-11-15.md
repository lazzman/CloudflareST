# IP 优选管理系统：POST-RC-01／02 修复复验

日期：2026-11-15。用户要求按[RC 修复后完整性复核](ip-optimization-post-rc-audit-2026-11-15.md)的建议修复。输入基线仍为 PRD v1.0＋v1.1／CR-001、UX R3、技术 R3，不修改产品规则。

## 1. 结论

**POST-RC-01（P2，通知重试保护）和 POST-RC-02（P3，延迟阈值一致性）均已修复并通过定向复验。**

- 原 Go 反例保持断言不变，3 个顶层用例全部通过；成功后再次引用旧失败尝试由 HTTP 202 改为 409，模拟通知累计从 3 次降为合法的 2 次，DNS 调用仍为 0。
- 新增通知回归验证两种通知渠道、过时尝试、最新合法重试、原连接、取消摘要、风险确认、并发和整批回滚。10 次竞态模式并发检查均为一个 202、一个 409，同号重放不新增投递。
- 前端 8 个文件、186 项测试通过；实际生产页面连接本机 Go 服务，60001ms 可创建保存、较大有限数可编辑回读，非法值在页面阻止提交。
- 全仓 Go、四重点包竞态、`go vet`、生产构建及旧缺陷回归通过。修复后的本地 Docker 镜像构建和断网容器冒烟通过。
- 两轮限定独立审查均为 9/10，未发现需要返工的阻断项；审查是源码和证据核对，不冒称独立重新执行全部测试。

本结论只关闭上述两项已知缺陷，不宣称全部 110 条 AC 的所有组合及真实环境均已签收；真实 DNS／消息联调、公网测速、完整 ICMP／IPv6 部署矩阵和长期压力仍需在授权环境验证。

## 2. 修复与回归

### POST-RC-01：服务端验证当前重试资格

`internal/management/connections.go:392` 在现有写事务中识别该运行、该通知渠道的最新尝试；被引用的原尝试必须同时符合：

1. 原尝试属于当前运行、同通知渠道及原服务连接。
2. 原尝试为 `failed` 或 `unconfirmed`，且没有任何后续同渠道尝试。
3. 用户已明确确认重发风险；取消运行仍只能重发对应的取消摘要。

后续同渠道尝试不论已成功、在途或已失败，都不允许旧尝试再次生成投递；其他通知渠道或 DNS 投递不会错误阻塞最新合法重试。该判断与新投递创建同事务执行，沿用现有全局写锁，原操作号幂等仍在业务资格校验之前返回原收据。

正式回归入口：`internal/management/notification_retry_test.go:15`。

| 场景 | 结果 |
| --- | --- |
| Telegram／企业微信已有后续成功、pending、dispatching、running、failed、unconfirmed 或 skipped 尝试 | 旧引用均拒绝；同渠道其他连接也不能绕过 |
| 最新失败／未确认尝试、普通结束／取消运行 | 风险确认后可重试；保留原原因、原连接、取消身份及操作号关联 |
| 未确认风险或尝试连接不匹配 | 拒绝创建新投递 |
| 其他通知渠道或 DNS 已有新投递 | 不影响当前渠道最新合法重试 |
| 两个新操作号并发重试同一失败尝试 | 只有一次受理；失败请求不新增投递或业务收据 |
| 同号在完成前、完成后重放 | 返回原收据，不再次调用通知适配器 |
| 两渠道批量请求，后一渠道引用过时尝试 | 整批回滚，前一渠道也不新发 |

这是业务重试资格保护，不是对外部消息服务提供“恰好一次”送达保证。所有通知均为替身调用，没有真实 Telegram／企业微信发送。

### POST-RC-02：前后端统一有限正数门槛

- `internal/management/web/src/lib/domain.ts:153` 移除延迟阈值的 60000 上限，保留数值类型、有限且大于零校验，错误提示同步更新。
- `internal/management/web/src/pages/TaskEditorPage.vue:66` 移除该输入框的 `max` 属性；不改变失败比例 100% 上限、探测超时或下载窗口规则。
- `internal/management/web/tests/domain.test.ts:66` 新增 23 项测试，覆盖原上限、60001、极小／极大有限正数，0、负数、非数字、无穷大及其他门槛不变。
- `internal/optimizer/config_test.go:62` 补齐相同后端边界；后端原实现已符合 PRD，无需修改测量算法。

新脚本 `scripts/test-management-post-rc-fixes.mjs:1` 使用当前生产资源和本机真实 Go 服务验证：60001 创建保存；0、负数、空值及溢出输入不产生写请求；1000000000 编辑保存并重新打开回读；320px／1280px、明暗两种主题无横向溢出。只创建和更新临时任务，不启动优选或计划，不配置 DNS／通知连接。

## 3. 证据与执行范围

证据目录：[post-rc-fixed-2026-11-15](post-rc-fixed-2026-11-15/README.md)。保留原失败审计目录及其文件，不覆盖历史证据；AC-73／AC-75 的当前状态在[追踪表](ip-optimization-ac-trace-2026-11-15.md)更新。

| 验证 | 证据 |
| --- | --- |
| 新增正式回归在修复前确实失败，修复后通过 | `before-fix-go.txt`、`targeted-go.txt` |
| 原始 Go 反例未改断言并转为通过 | `original-counterexamples.txt` |
| 延迟门槛后端边界 | `threshold-go.txt` |
| 并发竞态重复 10 次 | `concurrent-repeat-final.txt` |
| 全仓 Go／重点包竞态／静态检查 | `go-full.txt`、`go-race.txt`、`final-checks.txt` |
| 前端 186 项／生产构建 | `frontend-tests.txt`、`frontend-build.txt` |
| 本轮生产页面创建、拒绝非法值、编辑回读 | `browser-post-rc-ready.txt` |
| 原 F／GAP／RC 后端回归 | `f-regression.txt`、`gap-regression.txt`、`rc-regression.txt` |
| 原页面修复／RC 真实同源回归 | `browser-previous-fixes.txt`、`browser-rc-regression.txt` |
| Docker 构建、断网容器、镜像身份 | `docker-build-with-dependencies.txt`、`docker-smoke.txt`、`docker-image.txt` |

本机实际使用隔离 Chrome for Testing **149.0.7827.55**，不是上轮审计的 Chrome 157；没有连接用户浏览器资料。第一次页面尝试因默认浏览器程序缺失而退出，之后显式选择本机已有测试浏览器完成验证。原 `check-ui.mjs` 是等待旧错误出现的观测脚本，保持不变，不将它伪称为已转绿；新的真实同源脚本承担修复断言。

首次 Docker 离线构建因依赖缓存不足失败；开放构建阶段下载依赖后成功，容器运行验收仍使用 `--network none`。这些环境失败及后续成功分别留档，不混写为首次即通过。容器冒烟中的启动前连接拒绝和未认证 401 为预期检查，最终命令退出 0。

## 4. 本地交付物与后续边界

- 本地镜像：`cloudflarest-management:r3-post-rc-fixed`，`linux/arm64`。
- 镜像 ID：`sha256:82c46b0f25853b4db4295e64f0b75b06b8ac8c197880bbfc28cb34ac23cc98f0`。
- 已在本地 CI 配置加入原通知反例与真实页面脚本；未触发远端 CI，未推送镜像仓库。
- 临时服务、测试容器及卷已清理；本轮修改仅涉及两项修复、相应回归／CI 与复验文档，未重置其他既有工作。

复跑命令（页面测试前先生产构建；需要本机已安装的独立测试浏览器）：

```sh
go test -count=1 ./...
go test -race -count=1 ./internal/management ./internal/optimizer ./internal/integrations ./internal/safedata
go vet ./...
node docs/post-rc-audit-2026-11-15/check-go.mjs
(cd internal/management/web && npm test && npm run build)
node scripts/test-management-post-rc-fixes.mjs
docker build -t cloudflarest-management:r3-post-rc-fixed .
sh scripts/test-management-docker.sh cloudflarest-management:r3-post-rc-fixed
```

若默认浏览器不存在，可通过 `PLAYWRIGHT_EXECUTABLE_PATH` 显式指定测试程序，不使用用户的浏览器会话。

当前阶段：两项缺陷修复及定向复验完成。假设仍为共享管理密码、可信小团队、单实例单卷。下一阶段为受控部署及明确授权的真实集成验收；如发现新反例则回到对应实现修复，不据此变更原产品规则或放松外部操作保护。本轮没有真实 DNS 写入、消息发送、公网测速或 Git 提交／推送。
