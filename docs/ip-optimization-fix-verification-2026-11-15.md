# IP 优选管理系统：八项缺陷修复复验报告

> 范围提示：本文只关闭此前 F-01～F-08。之后的[完整性核对](ip-optimization-completeness-review-2026-11-15.md)发现另外七类缺口，其补齐结果见[最新复验报告](ip-optimization-completeness-fix-verification-2026-11-15.md)；本文原始通过证据保持不变，不能只据本文推断全部功能／环境已验收。

日期：2026-11-15。对象：当前未提交工作区中的管理系统实现。输入基线：PRD v1.0＋v1.1／CR-001、页面与技术设计 R3，以及同日的[修复前独立验收报告](ip-optimization-current-acceptance-2026-11-15.md)。

## 1. 结论与范围

**本轮 8 项已报告缺陷（1 项 P1、7 项 P2）均已修复，并通过对应反例、正式回归和定向页面复验，可以关闭这 8 项缺陷。**

原始 6 个 Go 失败反例保持断言不变，现已全部通过。另 3 项页面问题由模拟交互回归和当前生产资源＋真实 Go 服务双重验证。后端三个重点包共 98 个顶层测试、前端 144 项单测通过；全仓 Go 回归、竞态检查、静态检查、生产构建、浏览器回归和断网 Docker 冒烟均通过。

这不是“全部 R3／全部 110 条 AC 已逐条签收”或生产上线结论。真实第三方联调、最大容量压测和完整部署平台矩阵仍未验证，边界见第 7 节。没有修改产品决策或原始失败断言，没有提交、推送 Git 或触发远端 CI。

## 2. 缺陷关闭记录

| 缺陷与依据 | 修复措施 | 关键复验事实 | 结论 |
| --- | --- | --- | --- |
| F-01／P1：映射 CIDR 绕过地址族与预算；AC-38、AC-42 | 纯 IPv4-mapped CIDR 先规范化为等价 IPv4；混合 IPv6 范围采样排除映射空间；入池拒绝未规范化的映射地址，统一采样、计费与实际探测地址族 | 原反例中 IPv4 上限 1 时实际候选为 1；关闭 IPv4 后不产生 IPv4 候选；新增混合 /95、足额无重复采样与入池守卫回归通过 | 已关闭 |
| F-02／P2：Telegram 原地换机器人；AC-76 | 同时比较 Token 的机器人 ID 与 chat_id；同机器人密钥轮换保留，不同机器人／会话要求新建连接；事务内复查身份、revision 和活动引用 | 原换机器人请求返回 409，原连接身份保持不变；同机器人换密钥通过；不调用真实 Telegram | 已关闭 |
| F-03／P2：刷新后未决创建换号重复保存；R3 C-01 | 持久化原操作号与非敏感动作指纹，刷新／重新登录恢复动作锁；只读核查原号后才解除；凭据不进入持久化指纹；存储失败在发请求前拒绝 | 丢弃已提交创建的响应后，刷新并再点保存，任务总数仍为 1、创建 POST 总数仍为 1；无关设置可保存，核查原号仅 GET；旧格式未决记录有明确保守提示 | 已关闭 |
| F-04／P2：系统 UTC、页面仍显示上海时间；AC-24、AC-35 | 日期展示、日期筛选统一使用响应式系统时区；未知／无效时区不伪装成上海；夏令时不存在或重复的输入时刻明确拒绝 | 服务端 `2027-01-01T00:00:00Z` 在系统 UTC 下显示 `2027/1/1 00:00:00`；浏览器设为洛杉矶时，UTC／上海各页面展示与筛选仍同口径 | 已关闭 |
| F-05／P2：取消摘要人工重发被跳过；AC-73 | 明确关联原失败／未确认取消摘要的重发继承 Cancellation 语义；受理与发送统一识别取消请求及 cancelled 主状态；仍禁止取消后新业务通知 | 原反例中第二次模拟 Notify 确实调用，Cancellation=true；模拟渠道故意失败仍如实记 failed，不再静默 skipped；重启取消场景不能发新普通通知 | 已关闭 |
| F-06／P2：来源阶段取消丢失既有事实；AC-63 | 来源预建状态，取消返回已积累的来源池；保留解析结果、候选及已完成状态，当前与未开始来源标记取消 | 原反例保留 2 个来源及 1 个已解析候选；解析中取消、进度回调取消的正式回归通过，不将未测速候选记为合格 | 已关闭 |
| F-07／P2：扩采漏记其他命中来源；AC-09、AC-40、AC-48 | 新增或重复候选均补齐所有命中的已选来源，保留扩采触发信息，不重复计费或探测 | 原反例 256 个候选中落入所选 /28 的 16 个候选全部保留该来源，来源计数为 16 | 已关闭 |
| F-08／P2：缺少每 CIDR 独立数量入口；AC-22 | 每条 CIDR 提供独立数量；空值沿用默认，旧默认值 0 回填为空；显式输入只接受 1～100000 整数，保存与回填一致 | 两条 CIDR 的 5／8 分别保存回读；0、非整数及越界拒绝，空值默认；320px、双主题与修错焦点通过 | 已关闭 |

正式回归入口：

- 测量边界：`internal/optimizer/regression_test.go`、`internal/optimizer/mixed_cidr_test.go`。
- 身份与取消投递：`internal/management/review_regression_test.go`。
- 未决操作、时区、数量校验：`internal/management/web/tests/client.test.ts`、`domain.test.ts`、`datetime.test.ts`。
- 页面修复：`internal/management/web/tests/browser-fixes.mjs`。
- 真实生产资源／HTTP 反例复验：`scripts/test-management-fixes.mjs`。

## 3. 实际执行结果与证据

证据归档目录：[fix-verification-2026-11-15](fix-verification-2026-11-15/README.md)。原始失败输出仍在 `docs/acceptance-2026-11-15/`，未被通过输出覆盖。

| 验证层 | 实际结果 | 归档证据 |
| --- | --- | --- |
| 原始 Go 反例重放 | 6／6 通过；原断言不变 | [original-go-counterexamples.txt](fix-verification-2026-11-15/original-go-counterexamples.txt) |
| 全仓非缓存回归 | `go test -count=1 ./...` 通过，包含旧 CLI 相关包 | [go-full.txt](fix-verification-2026-11-15/go-full.txt) |
| 三个重点包测试统计 | optimizer 55、integrations 17、management 26，合计 98 个顶层测试；失败 0、跳过 0；不重复计子用例 | [go-test-summary.txt](fix-verification-2026-11-15/go-test-summary.txt) |
| 竞态回归 | management／optimizer／integrations 的 `-race -count=1` 通过 | [go-race.txt](fix-verification-2026-11-15/go-race.txt) |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 | [environment-and-final-checks.txt](fix-verification-2026-11-15/environment-and-final-checks.txt) |
| 前端单测 | 5 个文件、144 项通过 | [frontend-tests.txt](fix-verification-2026-11-15/frontend-tests.txt) |
| 前端生产构建 | TypeScript／Vite 通过，静态资源重新嵌入真实服务 | [frontend-build.txt](fix-verification-2026-11-15/frontend-build.txt) |
| 常规浏览器回归 | 任务／队列／详情／连接／日志、键盘焦点、320px／1280px、浅／深色通过 | [browser-regression.txt](fix-verification-2026-11-15/browser-regression.txt) |
| 修复专项页面 | F-03、F-04、F-08 及旧版未决记录兼容通过 | [browser-ui-fixes.txt](fix-verification-2026-11-15/browser-ui-fixes.txt) |
| 真实同源冒烟 | 嵌入页面、真实登录、保存／回读、日志筛选和退出通过 | [browser-live.txt](fix-verification-2026-11-15/browser-live.txt) |
| 真实修复反例与回环执行 | 任务不重复、UTC 显示正确、CIDR 数量分别保存；127.0.0.1 单候选 TCP 优选 completed，投递数 0 | [fixed-browser.txt](fix-verification-2026-11-15/fixed-browser.txt) |
| Docker 构建与隔离启动 | linux/arm64 镜像构建成功；断网、非 root、只读根、页面、未认证拦截、随机密码权限／重启轮换、主密钥保留通过 | [docker-build.txt](fix-verification-2026-11-15/docker-build.txt)、[docker-smoke.txt](fix-verification-2026-11-15/docker-smoke.txt) |

Docker 日志中的启动就绪前 connection refused 是轮询过程；未认证请求的 401 是预期拒绝，不是遗漏的失败。脚本只有所有断言通过才输出末尾“通过”结论。

## 4. 审查中追加关闭的同根因边界

- 映射地址：不仅覆盖纯映射 /120，还覆盖含映射 /96 空间的混合 /95，避免仅修输入形式而保留实际地址族越界。
- 取消状态：管理端独立审查第 1 轮发现重启后主状态 cancelled、CancelRequested=false 的遗漏；统一判断后，第 2 轮通过，定向评分 7/10 → 9/10。新增回归同时检查受理边界和发送守卫。
- 未决操作：持久化存储不可用时请求尚未发出；清理持久化失败时继续保留旧锁。凭据类动作不保存秘密或秘密摘要。
- 前端独立定向审查 1 轮通过，评分 9/10，并实际运行 144 项单测及 TypeScript 检查。评分仅补充本轮风险判断，不替代用例证据或生产安全审计。

## 5. 环境、隔离与清理

- 本机：macOS arm64，Go 1.26.2、Node 22.23.4、npm 10.9.9、Docker 29.9.3。
- 主验收浏览器：Chrome 157.0.8103.78 的隔离实例，不读取用户浏览器资料或使用已有登录会话。页面请求仅允许测试 origin。
- 所有凭据均为虚构数据；DNS、Telegram、企业微信使用替身；真实探测只针对本机回环 TCP，下载与投递关闭。软件依赖及镜像构建可访问软件源，不等同于业务第三方联调。
- 临时服务与数据由测试脚本清理；归档时再次检查：5187／5191 无监听，未发现本轮真实测试服务，`cfst-r3-test-*` 容器及卷均为空。未触碰生产数据卷。
- 已构建本地镜像：`cloudflarest-management:r3-fixed-acceptance`，平台 `linux/arm64`；镜像 ID 为 `sha256:fdfc9066dcb2489c2749da65b8e2bcdaba567b8eaeea857515f8513d3c4a48f1`。未推送镜像仓库。
- Docker 构建后无业务源码变更；之后的前端测试重新构建同一组源码。CI 补充了浏览器与原始反例回归，但仅修改本地配置，未触发远端 Actions。

## 6. 可重复执行入口

以下是复跑步骤，不要求重放已完成的业务操作。需有 Go／npm 依赖和 Playwright 浏览器；Go overlay 反例使用离线依赖模式，须先准备好 Go 缓存。浏览器使用独立实例，测试数据只写隔离目录；Docker 脚本自动移除自己的临时卷。

在仓库根目录执行：

```sh
(
  cd internal/management/web
  npm ci
  npm test
  npm run build
  npx playwright install chromium
  npm run test:browser
  npm run test:browser:fixes
  node tests/live.mjs
)
go test -count=1 ./...
go test -race -count=1 ./internal/management ./internal/optimizer ./internal/integrations
go vet ./...
node docs/acceptance-2026-11-15/reproduce-go.mjs
node scripts/test-management-fixes.mjs
docker build -t cloudflarest-management:r3-fixed-acceptance .
sh scripts/test-management-docker.sh cloudflarest-management:r3-fixed-acceptance
git diff --check
```

可通过 `PLAYWRIGHT_EXECUTABLE_PATH` 指定已有浏览器可执行文件，仍创建隔离实例而非连接用户会话。修复复验使用新的 `scripts/test-management-fixes.mjs`；修复前浏览器脚本与输出保留用于解释原缺陷，不作为当前签收结论。

## 7. 交接与剩余验证边界

- 当前阶段：缺陷修复与定向复验完成；上述 8 项无剩余阻断。
- 输入基线：PRD v1.0＋v1.1／CR-001、UX/UI R3、技术 R3；本轮没有新增产品假设或改变契约。
- 产出：修复代码、正式回归、真实同源复验脚本、本报告及不可混淆的前后证据。
- 已确认事实：第 2、3 节的用例与环境通过，不把测试数量等同于 AC 覆盖率。
- 假设：单实例、共享管理密码、可信小团队部署等原设计约束继续成立；本轮不扩展多租户或多实例能力。
- 发布前尚待补齐：真实阿里云 DNS 权限／写入与异常核对、真实 Telegram／企业微信可达性、部署网络下的测速与 ICMP 权限、最大候选容量／压力、目标平台（包括 amd64）以及完整 AC 的逐条签收。
- 风险影响：替身与本机验证不能证明真实凭据权限、第三方配额和服务商异常语义，arm64 容器通过也不代表所有部署平台已通过。
- 下一阶段：可进入隔离环境部署与发布评审；真实第三方联调和发布需单独安排，不能据此自动执行。
- 回退条件：若真实环境出现状态／外部写入契约差异，先返回技术方案或产品边界确认，并增加对应回归；本轮未授权真实外部变更。

基线 SHA-256（用于区分需求版本，不代表 Git 提交）：

| 工件 | SHA-256 |
| --- | --- |
| PRD v1.0 | `54ab3de84f4fab0290e5ba7cafb54399af3a105cbeacb092fd38179d39915438` |
| PRD v1.1 增补 | `04084f492304076c2a8f57b58a4b12511cb234bd971a035100a6e0120733c26c` |
| 页面设计 R3 | `01739cb734f676c729b8f73248c446a1d80b683ae36422eb548e58ef958e565a` |
| 技术设计 R3 | `3832640eb81e292ca6ffd3c6f0dcdef75db572e108693ccc27cd828c273fe88a` |
