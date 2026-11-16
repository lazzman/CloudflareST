# IP 优选管理系统 R3：实现交付与定向验收

日期：2026-11-15。输入：PRD v1.0、v1.1 增补 CR-001，以及页面／技术设计 R3。本文记录本轮真实执行的实现及验证，不替代正式线上验收、容量承诺或全部 AC-01～AC-110 的逐项签署。

## 1. 本轮交付

- 独立管理入口：Go HTTP 服务、Vue 3／TypeScript 页面、同源 API 和编译后嵌入资源。保留原 CLI，不通过解析终端输出驱动后台。
- 共享密码及最长 7 天会话、当前会话退出、来源级滚动登录冷却、CSRF 与可信代理范围；随机密码每次启动重新生成，使用业务卷以外的受限临时文件获取。
- 多任务、配置版本与运行快照、快捷计划和五字段 Cron、统一时区、持久化 FIFO、全局单席位、同任务防重、停止／取消、重启不重放。
- 分族来源组合、严格候选预算、去重与单轮邻近扩采、ICMP／TCPING、中位延迟、可选过滤、按候选 IP 建连的两窗口下载、分族最终名次、逐步骤详情及实际阶段计数。
- 阿里云 DNS 现存 A／AAAA 记录明确选择、仅替换 Value、保留现场 TTL／线路等属性、实际目标锁、最后已知确认写入、唯一只读核对及未确认保护。账号稳定身份通过 STS 只读验证；连接别名共享目标保护。
- Telegram／企业微信独立自动结束摘要、首次手动摘要与单渠道重发；不在保存连接时发送测试消息。连接凭据本地 AEAD 加密且不明文回读。
- 运行查询、详情、回收站与恢复、归档任务恢复后定时关闭、独立固定 30 天操作日志、历史保留设置。
- Docker 多阶段构建、非 root、只读根文件系统、临时密码目录、持久化数据卷与独占锁；提供 CI 定向验收任务。

技术取舍：模块化单体，SQLite 使用 modernc 驱动，进程内单消费者，根模块最低 Go 1.23；Docker 用 Go 1.26、Node 22 构建。前端采用原生 Vue 控件和共享组件，未引入 Element Plus。原 CLI 兼容矩阵最低 Go 工具链同步改为 1.23，未改写用户已有的 CLI 功能改动。

## 2. 实测结果

| 验证 | 结果与证据 |
| --- | --- |
| 全仓 Go 回归 | `go test ./...` 通过，包含原 task／utils 等测试 |
| 定向用例 | optimizer 45、integrations 17、management 21 个顶层用例通过，合计 83；子用例不重复计数 |
| 竞态 | `go test -race -count=1 ./internal/management ./internal/optimizer ./internal/integrations` 通过 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 前端单测 | `npm test`：4 个文件、98 项测试通过 |
| 生产构建 | `npm run build` 类型检查与 Vite 构建通过 |
| 浏览器交互 | 独立 Chrome 测试会话通过，覆盖 1280px／320px、浅／深色、表单边界、键盘错误焦点、导航／弹窗焦点、七主状态、分族结果、独立渠道及 AND 筛选 |
| 真实同源冒烟 | 临时 Go 服务＋本轮生产嵌入资源：登录、保存／回读仅 127.0.0.1 的 TCP 配置、空队列、日志对象＋结果筛选、320px 退出和会话失效通过；没有发起优选运行、DNS 或消息 |
| 镜像构建 | `docker build -t cloudflarest-management:r3-acceptance .` 通过，当前宿主 aarch64 |
| 容器运行 | `sh scripts/test-management-docker.sh` 通过：断网、UID 10001、只读根文件系统、嵌入页面、未认证 401、随机密码权限与重启轮换、卷内主密钥不变；临时容器和卷已自动删除 |
| 交叉编译 | management 包 Windows／386、Linux／386 及管理入口 Windows／amd64 编译通过；不等于这些平台的运行验证 |

本机 Playwright 默认版本缓存缺失，重跑时显式使用 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome；测试创建独立临时 profile，不读取用户浏览器资料。浏览器请求限于测试 origin，页面外网请求为 0。退出之后收到的受保护读取 401 是预期拒绝，测试仅对该窗口放行，其他 HTTP／脚本错误仍使测试失败。

## 3. 高风险覆盖与追溯

| 方向 | 主要验证 |
| --- | --- |
| C-01 | 同固定号相同请求返回原收据，不同请求冲突；跨重启收据保留；过期号不能重做；连接重试先找收据，再决定是否核验外部账号；accepted 不锁住无关动作 |
| C-02／AC-105～110 | 跨连接别名同目标保护；重启及运行清理不解除；自动命中跳过不等待；人工重试成功仍不解锁；明确解除需风险确认及当前版本；保护读取异常不发写入并结算为未发起失败 |
| 席位及停止 | 同任务不重复、全局 FIFO、DNS／通知尾部仍占位；结束前检查自动投递全部已有结论；重启旧 running 中断、旧 queued 取消、在途外部结果不重放 |
| 来源与测量 | 预算预检、分族不借额、重复只留来源、邻近范围约束、有效初始延迟前 10%、实际阶段计数、中位数、无样本与未测不当 0、下载独立前置／正文窗口、提前 EOF、停滞、真实读取失败、固定 Host／TLS SNI |
| 测量取消边界 | 排序进度回调中取消仍返回取消；IPv4 已测完后 IPv6 取消，既有有效初始数不会回退为 0 |
| 六项 R3 缺口 | 最近写入信息、去重预算、自定义下载 URL、首次手动摘要、当前会话退出、归档任务恢复均有实现与定向测试／浏览器覆盖 |
| 认证与秘密 | 源级五次失败冷却、滚动窗口、可信代理右向左验证、当前会话退出不影响其他会话、CSRF、加密连接只写不回读、随机密码临时存放且每次启动更新 |
| 历史与日志 | 活动保护、7 天回收站、恢复新保留锚点、不改原执行时间／不重放、操作日志独立 30 天；中断的实际结束时间未知时不伪造时间 |

测试位置：`internal/management/acceptance_test.go`、`internal/management/contracts_test.go`、`internal/optimizer/*_test.go`、`internal/integrations/*_test.go`、`internal/management/web/tests/*`。这些是实际自动化断言，不能把表中的主题数量等同为完整 110 条验收通过。

## 4. 审查与修复

完成独立审查与修复复核：第一轮指出连接固定号重试的外部校验顺序、代理来源合并问题；第二轮确认修复后指出保护读取异常可能遗留非终态投递；均已修复并加入回归。最终复核 9/10，在该检查范围内没有未关闭的 P1／P2。评分仅辅助说明当前实现审查，不是生产安全认证。

静态核查补充发现并修正：按族预算详情不足、排序回调后的取消遗漏、未完成族影响已完成族统计，以及页面状态字典、独立通知、日志结果筛选等前后端契约不一致。

## 5. 未实测及上线前条件

- 未向真实阿里云 DNS 发起写入，未发送真实 Telegram／企业微信消息；所有外部投递测试使用模拟服务或注入适配器。
- 未验证默认公网测速文件此刻可用性，未进行真实公网 IP 优选或跨网络性能比较。
- 未完成 Linux／Windows／macOS 的真实多协议、多地址族 ICMP 权限矩阵；容器已验证启动与权限配置，但公网 ICMP 路由、IPv6 连通性和目标应答仍取决于部署环境。
- 未进行最大 100000 候选、多年历史及高频计划下的容量／性能压测。当前为单主机单实例，不是多节点 HA；无吞吐、延迟或 RTO／RPO 承诺。
- 未完成屏幕阅读器、全量 WCAG 或人工安全渗透验证；本轮仅执行已列出的键盘与布局检查。
- 未推送 Git、未发布远端镜像、未更改用户真实连接配置；当前没有遗留测试容器、任务执行或对外推送。

建议先按 `ip-optimization-management-deployment.md` 启动受信任环境，由部署管理者填入自己的连接，再用明确测试记录完成真实第三方联调。首次联调仍应核对目标记录与风险确认，不能将本报告的模拟通过视为已获准修改任意真实 DNS。

## 6. 复现

```sh
go test ./...
go test -race -count=1 ./internal/optimizer ./internal/integrations ./internal/management
go vet ./...
cd internal/management/web
npm ci
npm test
npm run build
PLAYWRIGHT_EXECUTABLE_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' node tests/browser.mjs
PLAYWRIGHT_EXECUTABLE_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' node tests/live.mjs
cd ../../..
docker build -t cloudflarest-management:r3-acceptance .
sh scripts/test-management-docker.sh
```

非 macOS 环境可安装 Playwright 对应 Chromium，或将 PLAYWRIGHT_EXECUTABLE_PATH 指向本机可执行的测试浏览器；不要求接管用户已登录浏览器会话。
