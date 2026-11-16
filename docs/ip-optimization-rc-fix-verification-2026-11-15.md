# IP 优选管理系统：RC-01～05 修复复验

> 后续进展：RC-01～05 的关闭结论保留；后续发现的POST-RC-01（通知重试保护，P2）及POST-RC-02（配置校验一致性，P3）也已修复并通过定向复验，见[POST-RC修复复验](ip-optimization-post-rc-fix-verification-2026-11-15.md)。本报告仍不能作为“全部功能／所有真实环境已验收”的声明。

日期：2026-11-15。基线：PRD v1.0＋v1.1／CR-001、UX／技术 R3；修复对象为[再次完整性确认](ip-optimization-reconfirmation-2026-11-15.md)的五项问题。用户明确要求修复。

## 1. 结论

**RC-01～RC-05 已修复，并通过本轮定向复验。** 原后端反例保持断言不变，3 个顶层用例／4 个场景全部转为通过。人工恢复与运行原因通过当前生产资源＋真实 Go 服务验证，而不只依赖前端替身。

- 全仓 Go 回归、重点包竞态、静态检查通过。
- 四个重点包共 **118 个顶层测试通过**：management 45、optimizer 55、integrations 17、safedata 1；统计显式启用虚构候选容量用例，不重复计子用例。
- 前端 **8 个测试文件、163 项通过**；生产构建、常规／历史修复／分页专项／本轮真实同源浏览器均通过。
- Docker 镜像构建及断网、非 root、只读根、随机密码轮换、主密钥保留冒烟通过。
- 两轮限定范围独立审查 **7/10 → 9/10**，本轮指出的问题均修正；评分不替代全系统验收。

这关闭的是本轮五项缺陷，不是“全部110条AC的每个场景、每种部署环境均已通过”。真实云 DNS、Telegram／企业微信及公网测速仍未执行。

## 2. 逐项关闭与证据

| 问题 | 根因修复 | 实际验证 |
| --- | --- | --- |
| RC-01 敏感测速链接进入历史 | 前后端拒绝带认证 Token、签名、密钥及常见 CDN 鉴权参数的测速 URL；保留合法的 bytes／size／format 等普通参数，不偷偷删参后运行。旧任务／快照／日志／候选中的已识别秘密在升级时清理，受影响任务提示修正并暂停定时 | 原快照反例通过；保存拒绝、编码参数、普通参数保留、v2/v3旧库清理／回滚／重启测试通过；真实页面校验阻止提交 |
| RC-02 中断摘要状态错误 | 已结束记录优先用运行最终状态；优选结论另列，保留“优选完成、整次中断”两个事实。正常自动收尾仍用已确定优选结论，不产生状态互等 | 原两个中断场景通过；正常收尾回归通过；所有消息使用替身，无真实发送 |
| RC-03 收据保留已删完整快照 | 新运行类收据仅保留 run_id、状态及席位，不复制完整 Run；升级精简旧收据副本，保留原操作号、请求摘要及原结果状态，旧操作仍不可重放 | 原取消→回收→第8天清理→查收据反例通过；旧完成收据迁移测试通过，原哈希及状态不变 |
| RC-04 未决操作缺少人工出口 | 增加独立核查许可：先只读查原号、核对对象和范围、明确风险确认；服务端建立原号关联并只许可一次同范围新提交。实际动作仍需回表单手动提交，受原业务校验保护 | 认证、风险确认、范围冲突、旧号迟到拒绝、一次消费、幂等重查、重启／到期关联、响应丢失／本地存储失败测试通过；真实页面从未知创建恢复后只生成1个连接 |
| RC-05 运行级原因不显示 | 详情按层显示脱敏运行终止原因与优选阶段错误，不与候选／DNS／通知原因混淆；中断摘要预览使用最终主状态 | 原页面原因观测转为可见；真实无候选失败运行原因展示通过；320px／1280px与双主题检查通过 |

新增与修改的主要入口：

- `internal/safedata/url.go`、`internal/optimizer/config.go`、`internal/integrations/redact.go`。
- `internal/management/private_history.go`、`operation_recovery.go`，以及既有摘要、运行保存、API和启动接线。
- `internal/management/web/src/api/client.ts`、`components/OperationTracker.vue`、`lib/safeText.ts`、执行详情与配置提示。
- 正式回归：`internal/management/rc_regression_test.go`、`rc_private_history_test.go`、`rc_recovery_test.go`、`internal/safedata/url_test.go`；前端 `tests/recovery.test.ts`、`safeText.test.ts`。
- 真实同源专项：`scripts/test-management-rc-fixes.mjs`。

## 3. 人工恢复的安全边界

1. unknown／expired／404 不自动清锁，也不证明原操作未执行；已受理或已明确完成的动作不能领取新许可。
2. 当前浏览器先保存非敏感方法、路径和原操作号；旧记录可从服务端收据恢复范围，过期响应也提供经过检查的动作范围。确实未知时由用户明确选择动作与对象 ID，后端再次校验。
3. 用户勾选风险后，POST `/api/v1/operations/{priorId}/recovery` 只创建核查许可与关联日志，不执行优选、DNS、消息或新建对象。
4. 原操作的 State／Result 保持不变，另记录核查关联；未知原号建立墓碑，避免之后迟到的原请求被当新动作执行。
5. 下一次用户明确提交携带 prior_operation_id／recovery_id；许可消费与业务结果在同一事务。范围不符或已消费则拒绝；业务校验拒绝时事务回滚，不消耗许可。
6. 核查响应丢失时可以从原号或核查号只读找回。核查请求本身未抵达时，只有用户再次明确确认才复用同一个核查号，不自动重交业务、不另造核查号。
7. 读取已消费的许可不会把它重新装回可用状态。新的业务尝试如果再次未知，继续保留其新操作号并沿同一核查流程处理。

测试覆盖同浏览器刷新、旧格式待核查条目、存储写失败、旧收据过期和服务重启。这里的单次许可不承诺第三方远端恰好一次；原DNS／通知的未确认、目标保护和重试风险规则不变。

## 4. 升级与敏感数据处理

- **管理数据库版本现为4**。旧候选分页版本2，以及中间版本3，均会执行敏感历史清理与旧收据精简；清理阶段与升到版本4在同一事务内，失败不标记升级成功。
- 升级前停止实例并备份 SQLite 和匹配主密钥；不得用旧程序直接打开新库，回退应恢复升级前完整备份。
- 只处理当前代码可识别的认证参数／地址形态。检测覆盖 token、signature、auth_key、hdnts／hdntl／hdnea、jwt、hmac 等常见形式，不宣称能识别任意业务自定义字段中的秘密。不要在自定义测速 URL 中放凭据。
- 对受影响任务，整个敏感测速地址替换为明确的“已移除，需重新配置”标记，定时暂停；不静默删掉参数继续下载，也不自动恢复计划。普通下载 URL、其他任务、DNS／通知开关及已发生的外部事实不因此改变。
- 任务修正为安全地址后可保存并手动重开计划。历史中被移除的秘密不再回填；结果原因中可识别的秘密也脱敏。
- SQLite 开启 secure_delete，升级后做 WAL checkpoint/truncate；这不是介质级安全擦除证明。**已有外部备份、快照、系统缓存或过去导出的秘密不由本迁移清除**；如曾配置真实敏感URL，应轮换相关凭据，并妥善保护或清理旧备份。
- 本轮所有升级实测使用独立临时数据，不修改用户生产数据卷。

## 5. 独立审查结果

第1轮发现并追加修复两点：

- 鉴权参数名单遗漏 auth_key 和部分 CDN 形式：统一前后端识别并追加版本4清理，确保已到版本3的旧库也会再次处理；不把普通查询参数全部删掉。
- 旧 pending 没有范围时只支持创建类恢复：现可从成功／过期收据找回可信范围；完全未知时可选择非创建动作与对象 ID，只锁原范围，不连带阻止无关创建。

第2轮确认上述修复成立，范围内无新增阻断；独立执行的是前端纯内存验证与代码审查，主端另实际执行后端、浏览器和Docker验证，不混淆证据来源。

## 6. 复验证据

目录：[rc-fixed-2026-11-15](rc-fixed-2026-11-15/README.md)。原失败证据保留在 `docs/reconfirmation-2026-11-15/`，先前 F／GAP 证据也不覆盖。

| 验证 | 证据 |
| --- | --- |
| 全仓／竞态／顶层计数 | `go-full.txt`、`go-race.txt`、`go-summary.txt` |
| RC原始反例、旧GAP／F回归 | `rc-original-counterexamples.txt`、`gap-original-counterexamples.txt`、`original-six-counterexamples.txt` |
| 前端163项与构建 | `frontend-tests.txt`、`frontend-build.txt` |
| 常规、旧修复、真实同源及分页专项 | `browser-regression.txt`、`browser-previous-fixes.txt`、`browser-live.txt`、`browser-real-previous-fixes.txt`、`browser-completeness.txt` |
| 本轮真实人工恢复、URL拒绝、运行原因 | `browser-rc-fixes.txt` |
| 原UI观测重放 | `rc-original-ui-observations.txt`：运行原因已可见；原脚本未点击界面的“只读查询”，所以恢复按钮未展开，不用于判定新人工恢复流程；后者由真实同源专项证明 |
| Docker／最终检查 | `docker-build.txt`、`docker-smoke.txt`、`final-checks.txt` |

本机为 macOS arm64，Go 1.26.2、Node 22.23.4；隔离Chrome 157.0.8103.78。当前本地镜像 `cloudflarest-management:r3-rc-fixed`，linux/arm64，ID `sha256:b83afec2ad9e6b69ede04b96ea0d3b64097aa138f379bb113af73cf1f960135b`；未推送镜像仓库。

## 7. 复跑入口与交接

```sh
go test -count=1 ./...
go test -race -count=1 ./internal/management ./internal/optimizer ./internal/integrations ./internal/safedata
node docs/reconfirmation-2026-11-15/check-go.mjs
(cd internal/management/web && npm test && npm run build)
node scripts/test-management-rc-fixes.mjs
docker build -t cloudflarest-management:r3-rc-fixed .
sh scripts/test-management-docker.sh cloudflarest-management:r3-rc-fixed
```

Playwright可通过 `PLAYWRIGHT_EXECUTABLE_PATH` 指定测试浏览器，但不连接用户会话。CI已加入本轮原反例与真实同源脚本，本轮未触发远端CI。

- 当前阶段：本轮五项已知问题修复与定向复验完成。
- 输入基线不变；产出代码、回归、迁移和本报告，原失败记录可追溯。
- 已确认事实以第2～6节为界；没有把测试数量等同于全部AC签收。
- 假设继续为共享管理密码、可信小团队、单实例单卷。
- 剩余发布门槛：真实云DNS／消息联调、部署ICMP／IPv6、公网下载可达性、跨平台及长期负载未在本轮执行。
- 下一阶段可安排受控部署与授权测试对象联调；若发现新反例应继续按证据修复，不改变既定外部操作保护。

没有真实DNS修改或消息发送，没有公网测速，没有Git提交／推送；临时测试服务、容器及卷已清理。
