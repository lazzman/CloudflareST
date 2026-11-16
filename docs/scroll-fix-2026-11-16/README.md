# 滚动修复证据

日期：2026-11-16。检查对象为新建任务长表单，以及复用应用外壳的其他管理页面。说明见同级目录的 ip-optimization-scroll-fix-2026-11-16.md。

- before.txt：修复前原始滚动、遮挡、弹窗、历史位置和短窗口观测。
- review-before.txt：新增桌面嵌套锁用例修复前失败，其余21项通过。
- closed.txt：最终22项专项全部通过。
- tests-closed.txt、build-closed.txt：最终200项单元测试和生产构建。
- browser-final.txt、browser-regression.txt：主要交互、旧修复及真实同源回归。
- scaffold-regression.txt、completeness-regression.txt：全站60组布局与返回列表状态等回归。
- go-full.txt：Go全仓测试。

所有业务数据为本机临时夹具或替身，截图不代表生产任务。SCROLL_OBSERVE_ONLY=1仅用于保留修复前现象，不表示通过；正常CI必须使用默认断言模式。

运行 node scripts/test-management-scroll.mjs 可复验；SCROLL_OUTPUT_DIR 可指定截图目录，PLAYWRIGHT_EXECUTABLE_PATH 可指定独立测试浏览器。不要连接用户日常浏览器或发真实推送。
