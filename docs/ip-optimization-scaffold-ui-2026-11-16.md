# 管理端前端改版：基于 scaffold-master

日期：2026-11-16。用户明确要求以指定仓库前端重做当前管理端。本轮更新实际 Vue 前端、嵌入生产资源和本地 Docker 镜像，不是仅交一个脱离业务的原型。

## 1. 交付结果

- [单文件外观预览](ip-optimization-scaffold-preview-2026-11-16.html)：9个实际截图视图，手机默认显示手机版。图片、样式、切换脚本全部内嵌。它是截图预览，不连接后台、不提交业务操作；数据来自隔离测试夹具。
- 重做图标侧栏、顶栏、登录页、任务工作台；统一任务配置、队列、记录、详情、连接、日志和设置的面板、表格、表单及状态样式。
- 支持浅色、深色、跟随系统；本地记忆偏好，系统主题变化实时跟随。
- 保留已验收的认证、接口、配置、DNS／通知确认与操作号幂等规则；不引入无关账号、RBAC或演示统计。

## 2. 参考来源与实际复用

参考：<https://github.com/wbr664393425-lgtm/scaffold-master> 的 `scaffold-vue`。固定提交 `577079e9a347555fadddd3865fc0bbcdb00d9fb4`，通过GitHub接口读取，未运行参考仓库安装脚本或后端。

| 参考实现 | 本项目落点 | 适配方式 |
| --- | --- | --- |
| `styles/tokens.css` | `src/styles/scaffold-tokens.css` | 复用字体、浅深色、玻璃面、描边、圆角和层次令牌，补充本项目状态与可读性别名 |
| `data/icons.ts`、`components/Icon.vue` | `src/lib/icons.ts`、`components/AppIcon.vue` | 复用24网格线性图标、统一描边；装饰图标不重复朗读 |
| `layout/AppShell.vue`、`Sidebar.vue`、`Topbar.vue` | `src/App.vue`、`src/style.css` | 图标分组侧栏、位置提示、状态条、桌面折叠与手机展开；接本项目路由及认证 |
| `ui/PageHeader.vue`、`Panel.vue`、`business/ListPage.vue` | `components/PageHeader.vue`、`SurfacePanel.vue`、`pages/TaskListPage.vue` | 页面头、查询区、表格面板及工具区，不照搬无关批量操作或接口 |
| `views/Login.vue`、`Dashboard.vue` 版式与材质 | `pages/LoginPage.vue`、`TaskListPage.vue` | 双区登录、真实配置计数及执行席位卡片，不使用参考项目统计数据 |
| 本地外观模式 | `src/lib/appearance.ts` | 最小化实现主题选择、刷新保留和系统变化跟随 |

表中路径相对 `internal/management/web/`。这是布局／组件／令牌适配，不是只换配色或将Spring Boot／RBAC整套迁入；保留原Vue3、TypeScript、Vite依赖，不新增UI库、GSAP、远程字体或图片服务。

MIT许可原文在 `internal/management/web/third-party/scaffold-master-LICENSE`；完整分发声明在 `public/third-party-notices.txt`，已随构建进入 `dist/third-party-notices.txt`。原作者版权声明完整保留。

## 3. 页面与行为

- 应用外壳使用参考设计的玻璃侧栏、中性画布、蓝色主操作。导航按工作区／配置管理分组；桌面可折叠，手机可展开，Escape关闭后焦点返回菜单按钮。
- 顶栏只保留外观控制；状态条仍显示当前执行、等待数和时区；读取失败保留未知／过时提示，不伪装空闲。写入结果不确定时自动读取原操作收据，仍未确认只在对应任务、执行或连接页面提示。
- 登录仍使用共享密码，保留环境变量／随机密码说明、退出未确认恢复入口及密码清空；流程图明确不是实时网络状态。
- 工作台显示活跃任务数、已启用计划数、DNS自动推送任务数和全局执行席位，全部由已有API读取。没有虚构吞吐、趋势或成功率。
- 任务表保留地址族、计划、下次运行及三个独立推送开关；搜索按名称或ID匹配，不因任务有名称而无法按ID查找。
- 桌面表格保留比较能力；窄屏采用带字段标签的卡片，不删掉DNS／通知异常或关键操作。
- 全站保留表单错误摘要、内联错误、键盘焦点、版本冲突、未知结果提示与危险操作确认；未知结果不常驻顶栏，只在对应页面提示。
- 本地键 `cfst-appearance-v1` 仅保存 `system`／`light`／`dark`；不保存认证、秘密或业务请求。存储失败不影响当前页面主题切换。

后端／数据库／测速／DNS／通知适配器未修改，非前端源码摘要与2026-11-15基线一致。共享密码、单实例单卷、FIFO、完整收尾、取消／重启不重放、明确DNS记录及未确认保护、通知最新尝试限制保持不变。

## 4. 实际验证

证据目录：[scaffold-ui-2026-11-16](scaffold-ui-2026-11-16/README.md)。浏览器为隔离 Chrome for Testing 149.0.7827.55，不使用用户浏览器资料；所有业务测试只用临时数据库／替身。

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 前端单元与生产构建 | 9文件、195项通过；TypeScript／Vite通过 | `frontend-test-build.txt` |
| 原主要浏览器回归 | 表单、队列、DNS／通知、焦点、连接、日志、运行状态等通过 | `browser-regression.txt` |
| 改版专项 | 10页×1440／390／320×浅深色，共60组通过；主题持久化／系统跟随、导航键盘及ID搜索通过 | `scaffold-browser-verified.txt` |
| 旧修复及真实Go同源 | 原号恢复、时区／数量边界，真实登录／保存回读／退出通过 | `previous-and-live.txt` |
| RC／POST-RC／完整性页面 | 人工恢复、URL拒绝、运行原因、60001门槛、候选来源／分页、DNS明细、返回状态通过 | `rc-flow.txt`、`post-rc-flow.txt`、`completeness-ui.txt` |
| Go全仓 | 通过 | `go-full.txt` |
| Docker | 构建及断网、非root、只读根、静态资源、密码轮换、卷主密钥保留通过 | `docker-build.txt`、`docker-smoke.txt`、`docker-image.txt` |
| 自包含预览 | 9视图在320／1440离线可读，图片内嵌，无脚本错误 | `preview-check.txt` |
| 独立审查 | 源码与9张截图未发现阻断，最终9/10 | `review-notes.md` |

初次自测修正了外观选择器可访问名称、手机卡片间距；截图脚本明确等待路由标题，避免同名概览／详情误取上一页。最终结果基于修正后的版本。60组布局检查不等于所有移动浏览器或完整无障碍认证；审查分数也不是用户满意度调查。

## 5. 使用与发布边界

直接打开同目录 `ip-optimization-scaffold-preview-2026-11-16.html` 查看外观，无须启动服务。真正管理功能在已更新的应用中。

```sh
cd internal/management/web
npm run dev:mock
npm test
npm run build
```

`dev:mock` 仅是隔离界面预览，不能管理真实任务。正式构建仍嵌入Go服务，Docker入口不变。

本地镜像：`cloudflarest-management:r3-scaffold-ui`，`linux/arm64`，ID `sha256:7d0383a020adb7b038d688291006b13d74db8703fdbc418884957b17d33217bc`。已构建并做断网冒烟，未替换现有服务、未推送镜像或触发远端CI。

CI已加入 `scripts/test-management-scaffold.mjs`；截图默认在 `internal/management/web/test-results/scaffold/`，可用 `UI_SCREENSHOT_DIR` 改输出目录，`PLAYWRIGHT_EXECUTABLE_PATH` 指定独立测试浏览器。临时服务、测试容器与卷已清理。

下一步可在受控部署中查看实际使用体验。真实DNS／消息联调及公网测速仍沿用此前未完成的授权验收边界；本轮没有真实DNS写入、消息发送、Git提交／推送或上线部署。
