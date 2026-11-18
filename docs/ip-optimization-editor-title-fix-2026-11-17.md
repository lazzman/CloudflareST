# 编辑页卡片标题修复

日期：2026-11-17。用户截图指出“DNS 与通知（独立开关）”标题与卡片上边框重叠。

## 原因与修正

- 配置分区使用 fieldset/legend，原生 legend 将标题嵌入边框。单纯调整 margin 无法使它成为正常的卡片内标题。
- 仅将七个标签页面板改为 section + h2；保留原 id、v-show、tabpanel 及 aria-labelledby，内部地址族、通知渠道等真正的 fieldset 分组不变。
- 标题统一 16 px，正文间距 16 px，使用既有颜色、圆角与内边距；不引入负 margin、位移补丁或新滚动容器。
- 未修改接口、配置值、DNS／通知开关、保存、位置恢复或取消语义。

## 复验

- 先补几何断言复现：修复前标题距面板顶端为 0 px，未处于卡片内。修复后 42 组断言通过，桌面顶距 17 px，手机顶距 13 px，正文间距至少 16 px。
- scripts/test-management-editor.mjs：42 组布局、输入保留、六类错误跳转、保存和键盘通过；新增“标题在卡片内且不压正文”的断言。
- scripts/test-management-scroll.mjs：29 项滚动回归通过。
- npm test：11 文件、205 项通过；npm run build：TypeScript 与 Vite 生产构建通过。
- npm run test:browser：原表单、键盘、取消、收据和通知替身交互通过。
- [离线预览](ip-optimization-editor-title-preview-2026-11-17.html)：含浅深色桌面与手机实际截图。

源码及生产构建已更新，运行中的服务未替换。所有浏览器操作均使用隔离样例，无真实 DNS／消息写入；不声称其他浏览器或真实部署联调通过。
