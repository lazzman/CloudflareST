# 再次完整性核对证据

结论见[再次确认报告](../ip-optimization-reconfirmation-2026-11-15.md)：当前仍有 5 处未关闭问题；旧七类专项继续通过。

本目录仅包含测试与观测材料，不修改业务实现。Go overlay 使用旧测试夹具，凭据／IP／域名均为虚构值；不会执行真实 DNS、通知或公网测速。UI 使用独立浏览器，仅允许本地测试 origin。

- `check-go.mjs` 当前应非零退出，失败原因见 `go-counterexamples.txt`。
- `check-ui.mjs` 输出观测布尔值及未决操作状态，正常退出不等于功能通过。
- `previous-ui-regression.txt` 是上轮七类 UI 断言本轮继续通过的结果，不覆盖新增问题。

原始 F／GAP 失败与通过记录不在此目录覆盖或删除。
