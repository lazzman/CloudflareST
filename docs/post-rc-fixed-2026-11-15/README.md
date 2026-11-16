# POST-RC-01／02 修复复验证据

日期：2026-11-15。结论见[修复复验报告](../ip-optimization-post-rc-fix-verification-2026-11-15.md)。原失败证据位于 `docs/post-rc-audit-2026-11-15/`，本目录不覆盖原文件。

- `before-fix-go.txt` 证明新正式回归在原实现上失败；`targeted-go.txt` 证明修复后通过。
- `original-counterexamples.txt` 是原 Go overlay 反例保持不变后的通过输出。
- `threshold-go.txt`、`concurrent-repeat-final.txt` 为门槛边界和十次竞态并发专项。
- `frontend-tests.txt` 为 8 文件、186 项；`frontend-build.txt` 为当前生产构建。
- `browser-post-rc.txt` 是默认浏览器缺失的启动失败；`browser-post-rc-ready.txt` 是指定独立测试浏览器后的真实同源成功结果，两者均保留。
- `docker-build.txt` 是离线构建缓存不足的失败；`docker-build-with-dependencies.txt` 是允许构建阶段下载依赖后的成功结果。`docker-smoke.txt` 的未就绪／401 为预期探测，最终断网、非 root、只读根与密码轮换检查通过。
- `f-regression.txt`／`gap-regression.txt`／`rc-regression.txt`、页面回归日志记录旧修复未退化。
- `final-checks.txt` 记录最终 Go、静态检查、格式及脚本语法检查；`SHA256SUMS` 固定归档证据和关键实现文件的本轮版本。
- 所有业务测试使用临时数据库与替身；真实页面只写临时任务，不执行测速／推送。日志中的 2026-11-14 时间来自固定夹具，执行日期为 2026-11-15。

复跑入口见报告第 4 节。测试源码位于正式 Go／Vitest 用例和 `scripts/test-management-post-rc-fixes.mjs`；原审计页面观测脚本不变，修复后不再期待它等待的旧错误出现。
