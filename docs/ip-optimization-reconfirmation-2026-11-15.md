# IP 优选管理系统：再次完整性确认

> 历史记录（修复前）：本文保留 RC-01～05 的发现与失败证据。用户要求修复后，五项已完成定向复验，见[RC修复复验报告](ip-optimization-rc-fix-verification-2026-11-15.md)；原始证据未改写。

日期：2026-11-15。基线：PRD v1.0＋v1.1／CR-001、UX／技术 R3；对象为七类缺口补齐后的当前未提交工作区。本轮仅核对与生成证据，不修改业务代码。

## 1. 当前结论

**仍不能确认“全部功能已完整实现”。** 主体功能模块已有实现，上一轮七类缺口的专项检查仍通过；但此次沿异常恢复、历史副本、敏感配置和结束事实继续核对，确认了以下 5 处未关闭问题。

“107 个后端顶层测试＋147 项前端测试通过”和“七类缺口已关闭”是各自范围内的既有事实，不足以覆盖本次新增场景。当前不是只差真实第三方联调，仍有实现需要修正。

## 2. 未关闭问题

编号 RC-01～05 仅用于本轮，与旧 F／GAP 编号区分。

### RC-01：含认证参数的下载 URL 原样进入可读历史

- 优先级：高；保密边界问题。
- 位置：`internal/optimizer/config.go:159`、`internal/management/tasks.go:68`、`internal/management/tasks.go:172`；详情快照未做该字段保护。
- 事实：仅拒绝 URL 的 userinfo，不限制 query 中的 token。使用完全虚构的 `https://download.example.invalid/file.bin?token=FAKE_DOWNLOAD_TOKEN_DO_NOT_USE` 可以保存；入队快照内完整保留该参数，历史快照可以明文序列化和读取。
- 实测：`TestReconfirmationDownloadSecretMustNotEnterReadableSnapshot` 失败；日志只记录是否包含虚构 token，不曾访问该 URL。
- 依据：AC-63 的非敏感快照、R3 页面 `5／技术 `9.1 的 URL 与敏感信息边界。不能把“URL 没有 username/password”当成“不含凭据”。
- 建议：明确拒绝带认证材料的测速 URL，或将秘密部分按受保护凭据保存并在页面／快照／错误中脱敏；不能仅遮挡页面而继续明文保存历史。

### RC-02：中断记录的人工摘要没有真实结束状态

- 优先级：P2；结束事实错误。
- 位置：`internal/management/deliveries.go:362`、`internal/management/runner.go:25`、`internal/management/connections.go:377`。
- 事实：摘要直接采用 `run.Result.Status`，仅特殊处理取消。重启恢复后主状态虽是 interrupted，但人工通知会沿用优选阶段结论。
- 实测两种情况均失败：优选未保存时消息的“结束结论”为空；优选完成、自动收尾中断时显示 completed。均经过本地恢复逻辑、人工通知受理与替身 Notify，未发送真实消息。
- 依据：AC-72 的中断摘要与 AC-98 的已中断事实。应同时保留“优选已完成”和“整次执行已中断”，不能以前者替换后者。
- 建议：区分优选阶段结果和执行最终状态；正常自动收尾仍使用既定优选结论，不引入双方等待，但人工发送已结束记录必须显示真实最终状态。

### RC-03：回收站清理后，操作收据仍保留整份已删除快照

- 优先级：P2；历史清理不完整。
- 位置：`internal/management/runner.go:511`、`internal/management/service.go:331`、`internal/management/operations.go:17`、`internal/management/history.go:75`。
- 事实：关闭通知的排队执行被取消时，取消收据直接 completed，Result 保存整份 Run。后续收据精简只处理 accepted；运行回收站清理不处理 completed 收据里的副本。
- 实测：取消→删除→推进至第 8 天→清理后，原 Run 已不存在，但读取原操作号仍返回原快照中的虚构标记。`TestReconfirmationPurgeRemovesSnapshotFromCancelReceipt` 失败。
- 依据：AC-64 到期清理候选／快照／投递详情；C-01 需要保留动作身份及简要收据，不要求重复保留已删的整份历史。
- 建议：取消收据只保存简要身份及状态；对已有收据中的历史副本给出清理／迁移处理，同时保留幂等墓碑和必要简要日志，不允许旧操作重放。

### RC-04：未决创建操作缺少人工处理后的恢复出口

- 优先级：P2；安全恢复流程未闭环。
- 位置：`internal/management/web/src/api/client.ts:109`、`internal/management/web/src/api/client.ts:120`、`internal/management/web/src/api/client.ts:144`、旧顶栏操作跟踪组件（已于 2026-11-19 移除）:24。
- 事实：连接新建发生网络结果未知后，锁按 POST /connections 范围保存；查询原号返回 operation_unknown 或过期，锁保持不变。即使用户拟建另一个连接，仍被 ACTION_UNCONFIRMED 拒绝。当前核查界面只有关闭、只读查询和按号查询，没有“核对后明确发起关联新尝试”的流程。
- 实测：首次 NETWORK_ERROR → 原号 operation_unknown → 第二次（名称不同）ACTION_UNCONFIRMED；实际模拟 POST 调用仅 1 次，证明没有盲重发，但也没有正常产品入口恢复。
- 依据：R3 C-01 技术 `9.2／UX `11.2 同时规定“不自动换号”和“人工明确的新尝试保留原关联”。这里缺的是人工出口，不是要求 unknown／404 自动解锁。
- 建议：提供明确对象核查、风险确认、保留原号关联的人工新尝试机制；不要简单清 localStorage、自动换号或把未找到解释成未执行。

### RC-05：运行级错误／中断原因未显示在执行详情

- 优先级：P2；异常诊断展示不完整。
- 位置：`internal/management/web/src/pages/RunDetailPage.vue:126`、`internal/management/web/src/api/types.ts`。
- 事实：后端提供 `reason` 和 `result.error`，当前详情只呈现主状态、阶段、候选和投递原因，没有呈现运行级原因。候选自身的失败原因能显示，不代表来源获取／整体执行／重启原因已显示。
- 实测：使用符合 API 结构的失败记录，页面能显示“失败”，但运行终止原因与优选错误说明均不在 DOM 中；另核对重启恢复的实际原因“服务重启，未恢复执行、补跑或重放外部操作”，在无候选可承载原因的中断场景下，页面仅显示“已中断”，仍不显示该原因。没有把数据读取失败伪装成业务失败。
- 依据：PRD `3.8 的结果解释与 UX R3 `7 的错误恢复要求。
- 建议：在主状态附近显示脱敏的运行级原因和优选阶段错误，并说明与 DNS／通知异常的区别，不用错误改变已有成功结果。

## 3. 验证方式与证据

- 新 Go overlay 反例：3 个顶层用例、其中中断摘要含 2 个子场景，合计 4 个检查场景失败。只使用临时数据库、虚构值、替身引擎及适配器，不修改产品包。
- 隔离浏览器：真实 Vue 页面＋内存替身，确认 RC-04／05；Chrome 157.0.8103.78 独立实例，不使用用户已有浏览器资料。
- 本轮重放上一轮原 3 个 Go 反例与 `scripts/test-management-completeness.mjs`，仍全部通过。说明此次问题属于新增边界，不把上轮通过证据改写为失败。
- 两轮独立只读审查：第1轮在后端范围评分8/10，发现 RC-02／03，主端随后动态复现；第2轮确认 URL 秘密和未决恢复缺口，并要求把 RC-05 收窄为运行级原因遗漏，不可泛称所有失败原因都不可见。本报告保留该界线，评分不代表全功能签收。

材料目录：`docs/reconfirmation-2026-11-15/`。

| 文件 | 内容 |
| --- | --- |
| `management_audit_test.go.txt`、`check-go.mjs` | 可重放的后端契约反例 |
| `go-counterexamples.txt` | 本轮实际失败输出 |
| `check-ui.mjs`、`ui-observations.jsonl` | 异常详情与未决操作恢复的页面观测 |
| `previous-ui-regression.txt` | 上轮七类 UI 专项本轮仍通过的输出 |

```sh
node docs/reconfirmation-2026-11-15/check-go.mjs
PLAYWRIGHT_EXECUTABLE_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' node docs/reconfirmation-2026-11-15/check-ui.mjs
```

Go 当前非零退出是契约反例不满足，不是编译失败。UI 正常退出只表示完成观测，须阅读逐项布尔结果，不能解读为全部通过。

## 4. 对现有交付判定的修正

- 主模块齐备：认证、多任务、来源／测量、计划队列、DNS、通知、历史及 Docker 的主体实现仍成立。
- 七类已知缺口：原定向断言通过；不扩大为覆盖所有跨状态、跨副本和恢复流程。
- 全部功能完整：当前不通过，至少需关闭 RC-01～05。
- 全部上线验收：除上述实现问题外，真实第三方、部署 ICMP／IPv6、跨平台及长期负载仍待验证。

本轮没有修改业务代码、提交／推送 Git、修改真实 DNS、发送消息或公网测速。下一步应集中修正上述已证实问题，再重放这些反例和原回归；不应再用“主体模块都有”替代“所有已确认契约均闭环”。
