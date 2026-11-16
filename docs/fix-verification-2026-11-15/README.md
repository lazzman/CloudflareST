# 2026-11-15 修复复验证据

结论与缺陷映射见[修复复验报告](../ip-optimization-fix-verification-2026-11-15.md)：8 项缺陷关闭；不代表全部 AC 或生产联调完成。

## 证据说明

- `original-go-counterexamples.txt`：原始 6 个反例在修复后重放的原始输出，断言未修改。
- `go-full.txt`、`go-race.txt`：全仓与竞态执行输出。
- `go-test-summary.txt`：从真实 Go JSON 事件提取的顶层测试名称及结果，55＋17＋26＝98；保留原事件文件摘要，不将子用例重复计数。
- `frontend-tests.txt`、`frontend-build.txt`：144 项前端测试及生产构建输出。
- `browser-regression.txt`、`browser-ui-fixes.txt`、`browser-live.txt`：常规页面、修复专项和真实同源脚本输出。
- `fixed-browser.txt`：真实 Go 服务上的 F-03／F-04／F-08 与回环优选逐项结果。
- `docker-build.txt`、`docker-smoke.txt`：镜像构建与断网启动结果；启动前轮询连接拒绝和未认证 401 为预期输出。
- `environment-and-final-checks.txt`：归档时实际执行的版本、静态检查、镜像身份及临时资源清理核对，带退出码。
- `SHA256SUMS.txt`：以上 13 份证据的文件摘要。可在本目录运行 `shasum -a 256 -c SHA256SUMS.txt` 检查。

原始失败输出保留在 `../acceptance-2026-11-15/`，本目录不覆盖它们。日志中的网络数据与凭据为虚构或回环测试数据；没有真实第三方投递。
