# 当前结果独立验收报告

> 历史记录（修复前）：本文保留发现缺陷时的结论、原始断言和失败证据，不表示最新工作区仍存在这些失败。8 项缺陷的修复措施及通过证据见[修复复验报告](ip-optimization-fix-verification-2026-11-15.md)。

日期：2026-11-15。对象：当前未提交工作区中的管理系统实现。需求基线：PRD v1.0＋v1.1／CR-001、页面与技术设计 R3。

## 1. 验收结论

**未通过完整验收，暂不建议按“R3 全部完成”签收或上线。**

既有回归、生产构建、浏览器常规路径及 Docker 冒烟均通过；但追加边界验收确认 **8 项未关闭问题：1 项 P1、7 项 P2**。其中 5 个后端／测量根因通过 6 个失败测试用例复现，另 3 项通过临时真实 Go 服务＋独立浏览器验证。不是依赖推测、旧报告评分或仅按测试数量下结论。

本轮不修业务代码，不改 PRD，不调用真实第三方投递。仅新增本报告与复现材料。之前的实现自测报告保留为历史记录，其通过结论不覆盖本轮新发现。

## 2. 本轮实测通过项

| 项目 | 结果 |
| --- | --- |
| 全仓非缓存回归 | `go test -count=1 ./...` 通过 |
| 定向竞态回归 | `go test -race -count=1 ./internal/management ./internal/integrations ./internal/optimizer` 通过 |
| 既有定向测试范围 | optimizer 45、integrations 17、management 21 个顶层用例；新增反例不混入这 83 个通过数 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 前端单测与构建 | 4 个测试文件、98 项测试通过；生产 TypeScript／Vite 构建通过 |
| 常规浏览器交互 | 既有脚本通过，包括 320px／1280px、键盘焦点、浅／深色、七主状态、分族结果、通知渠道、日志筛选 |
| 真实 HTTP 冒烟 | 当前生产嵌入资源＋临时 Go 服务：登录、保存／回读、队列、日志筛选和退出通过 |
| 新增真实回环执行 | 本地 TCP 服务＋仅 127.0.0.1 来源、IPv4 预算 1、下载和全部投递关闭：最终 completed，候选 1，合格且排名 1，投递 0 |
| Docker | 当前代码镜像构建通过；断网、非 root、只读根文件系统、随机密码权限／重启轮换、主密钥保留通过。独立临时容器和卷已清理 |

浏览器使用本机 Chrome 157.0.8103.78 的隔离测试实例，不连接用户已有会话。页面仅允许测试 origin；第三方 DNS／通知使用替身；新增真实探测仅针对回环监听端口，没有公网测速。

## 3. 缺陷清单（按影响排序）

### F-01／P1：IPv4-mapped CIDR 绕过地址族开关及累计上限

- 依据：PRD 第 3.5.1／3.5.2 节，AC-38、AC-42。
- 位置：`internal/optimizer/config.go:91`、`internal/optimizer/planning.go:276`、`internal/optimizer/planning.go:314`。
- 复现：双栈开启，IPv4 上限 1、IPv6 上限 10；来源为定向 `198.51.100.10` 及 `::ffff:192.0.2.0/120`，该段请求 4；关闭下载，使用假探测成功结果。
- 实际：产生并探测 **5 个 IPv4 候选**，仍报告 completed；预算却显示 IPv4 Used=1、IPv6 Used=0。关闭 IPv4 的另一反例仍生成 4 个 IPv4 候选。
- 影响：不是单纯显示错误，配置允许的地址族和实际探测总量失去约束。
- 原因：CIDR 规划以 IPv6 计数，添加候选时又 Unmap 为 IPv4，两个阶段的规范化口径不一致。
- 修复验收：入口统一规范化或明确拒绝映射 CIDR；规划、候选与实际探测使用同一族标识；两个反例通过且普通 IPv4／IPv6 预算回归不变。

### F-02／P2：Telegram 可原地换机器人，绕过新连接／重新绑定要求

- 依据：PRD 第 3.10 节，AC-76。
- 位置：`internal/management/connections.go:127`、`internal/management/connections.go:165`。
- 复现：空闲连接已被任务引用，chat_id 不变，将虚构 Token 的机器人 ID 从 100001 换成 200002，携带当前 revision PATCH。
- 预期：目标机器人身份变化应拒绝原地替换，要求新建连接再绑定。
- 实际：HTTP 202、凭据已更换、任务仍引用同一连接；未调用真实 Telegram。
- 修复验收：区分同一机器人凭据轮换与机器人身份替换；后者拒绝，前者保留既定空闲／引用限制。

### F-03／P2：刷新后未恢复未决创建操作，可换号重复保存

- 依据：R3 C-01；页面稿第 4.4、11.2 节，技术稿第 9.2 节。
- 位置：`internal/management/web/src/api/client.ts:14`、`internal/management/web/src/api/client.ts:23`、`internal/management/web/src/api/client.ts:90`。
- 复现：真实 POST /tasks 已保存并返回 202；测试层只丢弃该响应，页面显示网络结果未知。刷新后按原内容继续点击保存，未读取原收据、也未明确选择另建一个任务。
- 实际：旧操作号仍在本地待确认列表，但内存 actionLocks 没恢复；第二次生成不同操作号，任务数由 1 变 2。
- 判定边界：第二次请求由用户点击，不是自动重试；服务端同号幂等仍有效。问题是恢复流程将“继续完成未决操作”无提示地变成新动作。用户已确认原结果且明确另建任务则是合法新操作，不属于此缺陷。
- 修复验收：刷新／重新认证后的未决动作可关联原号、自动读取原号收据；不自动新号重交，不持久保存秘密参数，也不阻断无关动作。
- 2026-11-19 CR-003 更新：改为自动读取原号收据，删除顶栏全局待确认入口与人工恢复许可；异常只在对应页面提示。

### F-04／P2：系统时区为 UTC，下一次执行时间仍显示北京时间

- 依据：AC-24、AC-35；系统时区与计划显示应一致。
- 位置：`internal/management/web/src/lib/domain.ts:43`、`internal/management/web/src/pages/TaskOverviewPage.vue:25`。
- 复现：系统时区保存 UTC，计划 `0 0 1 1 *`；服务器 next_run_at 为 `2027-01-01T00:00:00Z`。
- 实际：顶栏显示 UTC，概览显示 `2027/1/1 08:00:00`；正确的 UTC 显示应为 `2027/1/1 00:00:00`。后端计划计算本身没有在此反例中出错。
- 原因：日期格式化默认 Asia/Shanghai，调用页面没有传入当前系统时区。
- 修复验收：计划、记录、日志和目标写入时间按明确展示的时区格式化；切换 UTC／Asia/Shanghai 不改变已存时刻。

### F-05／P2：取消摘要失败后，手动重发被受理却始终跳过

- 依据：PRD 第 3.11 节、AC-73；取消摘要不自动重试，但失败渠道可经人工风险确认重发。
- 位置：`internal/management/connections.go:275`、`internal/management/connections.go:380`、`internal/management/deliveries.go:92`。
- 复现：启用 Telegram，取消排队运行，模拟取消摘要失败；携带失败尝试 ID、risk_acknowledged=true 手动重发。
- 实际：HTTP 202，但新尝试 Cancellation=false，状态 skipped；模拟 Notify 次数仍为 1，没有第二次发送。
- 原因：新尝试未继承原取消摘要语义，落入“取消后禁止新业务投递”的拦截。
- 修复验收：只允许明确关联的取消摘要人工重发，不重新开始测速、DNS 或其他业务消息；保留原失败事实。

### F-06／P2：来源获取中取消会清空已取得的来源事实

- 依据：AC-63、PRD 第 3.8／3.9 节。
- 位置：`internal/optimizer/planning.go:78`、`internal/optimizer/planning.go:95`、`internal/optimizer/engine.go:48`。
- 复现：第一个域名假解析成功得到 `192.0.2.10`，在第二个域名解析时取消。
- 实际：进度已报告完成 1 个来源，返回结果却 Sources=[]、Candidates=[]；已取得的解析地址与来源状态消失。
- 修复验收：取消返回已取得的部分来源池，未处理来源明确标为取消／未开始；不得把未测速候选伪装成已合格。

### F-07／P2：扩采进入另一已选 CIDR 时漏记来源

- 依据：AC-09、AC-40、AC-48。
- 位置：`internal/optimizer/engine.go:499`、`internal/optimizer/engine.go:305`。
- 复现：来源一为定向 `192.0.2.200`，来源二为 `192.0.2.0/28`、初采 1；预算 300。定向地址假延迟 1ms、其余 10ms，故由它触发完整 /24 扩采。
- 实际：最终 256 个候选中有 16 个落在所选 /28，但只有 1 个保留来源二标记，另外 15 个遗漏，来源二 CandidateCount 错报为 1。
- 修复验收：扩采生成地址后补齐命中的全部已选来源，保留触发来源和生成方式，且不额外占预算、不重复探测。

### F-08／P2：页面缺少按 CIDR 覆盖初采数量的入口

- 依据：PRD 第 3.5.1 节、AC-22。
- 位置：`internal/management/web/src/pages/TaskEditorPage.vue:58`、`internal/management/web/src/pages/TaskEditorPage.vue:113`。
- 复现：新建任务，在一条来源行选择 CIDR 并填写网段，检查是否有该来源独立的抽样数量入口。
- 实际：来源行只有类型和值，没有数量控件；只能改统一 sample_size。真实 DOM 中来源行数字输入数为 0。后端 Source.Count 已存在，但管理页面不能配置该已确认能力。
- 修复验收：允许每个 CIDR 独立覆盖，空值沿用默认；有效范围 1～100000，保存、回读及快照一致。

## 4. 新增反例与复现材料

材料目录：`docs/acceptance-2026-11-15`。

- `reproduce-go.mjs`：使用 Go overlay 将临时测试附加到两个包，不写业务目录；当前执行 **6 个测试均 FAIL**，分别覆盖 F-01 两个反例及 F-02、F-05、F-06、F-07。非零退出是当前验收未满足预期，不是工具安装失败。
- `optimizer_audit_test.go.txt`、`management_audit_test.go.txt`：断言源码，全部使用替身，不真实联网。
- `optimizer-counterexamples.txt`、`management-counterexamples.txt`：主验收方重放输出。
- `browser-boundaries.mjs`：临时真实 Go 服务＋隔离 Chrome，复现 F-03、F-04、F-08，并执行单 IP 真实 TCP 回环正常路径。只允许测试 origin，DNS／通知开关关闭。
- `browser-boundaries.jsonl`：浏览器实际观测，包含任务数 1→2、UTC／08:00 错配、无 CIDR 数量控件，以及单 IP completed 的正常结果。

在仓库根目录执行：

```sh
node docs/acceptance-2026-11-15/reproduce-go.mjs
WORK=$(mktemp -d /tmp/cfst-acceptance-replay.XXXXXX)
node docs/acceptance-2026-11-15/browser-boundaries.mjs "$WORK"
```

Go overlay 脚本关闭依赖联网下载，需本机已有依赖缓存。浏览器脚本要求当前生产 dist 已构建，使用 macOS 本机 Chrome 的隔离实例；每次使用新的 WORK 目录。它在结束时停止进程，保留临时数据库／截图供检查，目录中仅含虚构验收数据。浏览器脚本发现上述缺陷时返回退出码 2。

## 5. 边界、依据与后续

- PRD v1.0 SHA-256：`54ab3de84f4fab0290e5ba7cafb54399af3a105cbeacb092fd38179d39915438`；v1.1 增补：`04084f492304076c2a8f57b58a4b12511cb234bd971a035100a6e0120733c26c`。
- 未运行真实云 DNS 写入、真实 Telegram／企业微信发送、默认公网文件测速、最大规模压力测试或完整跨平台 ICMP 权限矩阵。不得把它们列为通过。
- 正常路径与边界失败分别记录：已有 83＋98 项通过并不能覆盖本轮追加的数据组合及刷新／取消场景；本结论也不声称穷尽全部 110 条 AC。
- 建议先修 F-01（地址族／预算），然后 F-02／F-03（连接身份／未决操作），再修其余功能一致性问题。将这批反例纳入正式回归，并重跑页面＋容器验收后再申请完整签收。
- 当前交付阶段：实现后验收；阻断项：本报告 F-01～F-08；下一阶段：按缺陷修复并复验，仍不进入正式发布。若需改变已确认产品语义，应先回到 PRD 变更，而非修改断言使测试变绿。
