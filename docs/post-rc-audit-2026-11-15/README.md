# RC 修复后功能完整性复核证据

日期：2026-11-15。结论见[完整性复核报告](../ip-optimization-post-rc-audit-2026-11-15.md)。此目录新增两项缺口的反例及本轮回归日志，不覆盖历史 F／GAP／RC 证据。

## 复现入口

在仓库根目录执行：

```sh
node docs/post-rc-audit-2026-11-15/check-go.mjs
node docs/post-rc-audit-2026-11-15/check-ui.mjs
```

- Go 脚本通过临时 overlay 注入 `management_audit_test.go.txt`，禁用依赖网络下载，结束后清理临时文件；不写生产包。当前预期是两个通知保护反例失败、一个后端正有限阈值保存用例通过，不应包装为全绿验收。
- 页面脚本使用实际 Vue 表单和本机替身 API，隔离浏览器只允许本机测试 origin；结束后关闭浏览器和 Vite。脚本正常退出表示观测成功，`ui-observations.jsonl` 中 `uiBlocksSave: true` 表示契约缺口仍在。
- 通知全部使用虚构凭据和替身；在途子场景事务回滚，无真实消息、DNS 或公网测速。日志中 2026-11-14 的运行时间来自固定夹具，不是本次执行日期。
- `new-counterexamples.txt` 是新反例原始执行输出；`go-full.txt`、`frontend-tests.txt` 及 `f-replay.txt`／`gap-replay.txt`／`rc-replay.txt` 是本轮旧用例重放结果。
- `real-rc-flow.txt` 对应 `scripts/test-management-rc-fixes.mjs`；`gap-ui.txt` 对应 `scripts/test-management-completeness.mjs`。前者使用本机真实 Go 服务，后者采用隔离替身 API。
- 八份日志由本轮临时目录原样归档并核对字节一致；旧修复报告的 Docker／竞态等未在本轮重新执行，见原报告，不混记为本轮结果。
- `SHA256SUMS` 固定本轮证据与两个问题的关键源码／需求依据；后续业务修复会使对应源码哈希变化，这是正常版本变化，不应覆盖本轮快照。
