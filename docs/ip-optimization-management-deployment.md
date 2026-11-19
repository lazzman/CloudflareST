# IP 优选管理系统 Docker 部署

## 状态与入口

管理入口和旧命令行入口并存。生产前端由 Docker 多阶段构建生成并嵌入 Go 二进制；容器运行时不需要 Node.js。管理服务默认只绑定本机 127.0.0.1:8080（Compose 主机端口），优选系统所有使用者共享任务和管理密码。

GitHub 自动构建、GHCR 镜像标签及权限要求见[Docker 镜像发布说明](github-docker-image-workflow.md)；下文默认仍使用本地 Compose 构建。

## 启动

1. 在仓库根目录运行 `docker compose build`。
2. 可选地通过容器管理平台设置 CFST_ADMIN_PASSWORD。未配置时服务每次启动随机生成新密码，并写入 /tmp/cfst/admin-password（容器 UID 10001、目录／文件权限 0700／0600）；不会进入业务数据卷，也不会在 API、网页或普通日志输出密码。进入本机容器管理环境读取一次：`docker compose exec -u 10001 cfst-web cat /tmp/cfst/admin-password`。容器重建、重启或停止后随机密码失效；服务启动后读取本次新值。
3. 运行 `docker compose up -d`，访问 http://127.0.0.1:8080。

自定义密码应由容器管理平台的 Secret/environment 管理能力注入，不要把明文提交到仓库或命令历史。服务进程启动时读取 CFST_ADMIN_PASSWORD；变更它并重启会使旧会话失效。只面向可信小团队，不提供多租户隔离。

远程访问应置于受信任的 HTTPS 反向代理之后，并将 CFST_SECURE_COOKIE=true；不要直接把不加密的 HTTP 管理端口暴露到互联网。Compose 仅发布至宿主机 loopback。若代理会转发登录来源，请通过 CFST_TRUSTED_PROXIES 明确配置其容器 CIDR（可逗号分隔）；默认不信任任何代理头。反向代理必须删除来访客户端自行提供的转发头并自行生成规范的 X-Forwarded-For 链，不要信任整个 LAN／Docker 网段内不受控的主机。

NET_RAW 仅为 ICMP 探测所需的能力；若容器仍无法创建 ICMP socket，界面会明确提示，需手动切换 TCPING，不会自动降级。容器网络限制可能影响 ICMP，须在部署环境实测。

### 绑定挂载权限

挂载宿主机目录到 `/var/lib/cfst` 时，服务保留已有目录的属主和权限，不要求目录归容器用户所有或为 `0700`。容器用户 `10001:10001` 仍须能进入目录、读写所需文件并使用文件锁。只挂载可信的专用目录，不要让其他不可信用户写入。密钥和数据库必须能按 `0600` 保护。

如果 Docker 自动创建了仅 `root` 可写的宿主机目录，服务仍无法写入。若不想修改宿主机目录权限，可改用 Compose 默认的命名卷。已有数据应先备份并成对迁移 SQLite 数据库和 `master.key`。新建数据目录默认采用 `0700`；服务不会为访问挂载目录而以 `root` 运行。

## 日常使用：策略 → IP 结果

1. 在“优选策略”中新建或编辑策略，分别切换“基本信息／筛选条件／定时计划”填写配置。每次只显示一个分区，切换不保存也不清空输入；底部保存条始终可达。保存不会立即运行或自动开启定时。
2. 回到策略列表，直接打开“定时”开关；需要立即验证时点击“运行一次”。
3. 点击策略名称或“查看结果”，直接看到最近一次运行的 IP 列表。点击延迟、下载速度、失败比例列头切换排序；排序先作用于全部匹配 IP，再分页，不改变原优选排名及 DNS 选 IP。
4. 在结果顶部选择运行时间即可看历史；失败、无合格结果会如实显示，不会悄悄换成较早的成功记录。

默认只展示核心流程。测速细节、预算与 DNS／通知配置收在编辑页“高级设置”，队列、连接、日志和设置收在“高级管理”。已有自动推送、配置和历史保持不变；结果异常仍会提示。高级诊断及旧链接继续可用，不再是查看 IP 的必经路径。详见[简化范围说明](ip-optimization-prd-v1.2-simplification.md)。

策略行操作直接平铺，不收进“更多”。“查看结果／运行一次／编辑”为常用按钮；定时开关及“高级管理／归档策略／恢复策略”常驻显示，手机端自然换行。归档仍须确认，运行中不可归档；恢复后定时保持关闭，不自动执行。详见[策略操作复验](ip-optimization-actions-menu-2026-11-17.md)。

编辑页高级设置分为“测速细节／下载设置／抽样预算／DNS 与通知”，不再一次展开所有字段。保存会校验全部分区：发现错误时自动显示对应区域，错误摘要可跳转定位；字段值、计划开关和已选推送目标不会因分区隐藏而丢失。分区支持方向键及 Home／End 切换，Tab 进入当前字段。详见[编辑页分区复验](ip-optimization-editor-2026-11-17.md)。

## 数据与升级

当前管理数据库版本为4（此前候选分页为版本2）。升级会精简旧取消收据的完整运行副本，并清理已识别的敏感测速URL：受影响任务的地址标记为需重新配置、定时暂停，普通URL和其他任务不变。修正安全地址后须手动重开计划，不自动重跑／投递。版本2／3迁移与回滚已做隔离验证，细节见[RC修复复验报告](ip-optimization-rc-fix-verification-2026-11-15.md)。升级前停服务并备份数据库和匹配主密钥；回退使用升级前完整备份，不用旧程序开新库或手工重置版本号。已有外部备份不随迁移清除；若曾使用真实秘密URL，应轮换相关凭据并保护旧备份。

命名卷 cfst-data 保存 SQLite 数据库和加密主密钥；随机密码只在容器内存临时文件系统中保存，不在卷备份中。备份必须同时保存数据库和 master.key；二者不匹配时连接密钥不可恢复，程序拒绝静默生成新密钥。恢复卷不会恢复旧随机登录密码、未完成执行、旧推送动作或解除 DNS 写入保护的状态。升级前停止服务并备份，使用新镜像启动同一卷；不要用旧程序打开较新数据库，也不要多实例共享卷。实例锁拒绝第二个执行者。

运行数据及执行配置包含候选 IP 和目标信息；连接凭据使用本地 AES-GCM 密钥加密，不等同于卷级密钥托管。务必保护 Docker 主机与备份。管理会话是共享身份，不构成个人操作归属证明。

## 验收命令

RC-01～05 已修复；可运行 `node docs/reconfirmation-2026-11-15/check-go.mjs` 和 `node scripts/test-management-rc-fixes.mjs` 复验。未知操作先到“操作核查”读取原号，再核对实际对象、确认范围与重复风险，领取一次同范围新尝试许可后回表单手动提交；核查本身不执行业务，不可用清浏览器存储替代。

七类完整性缺口已补齐并复验；逐条实现／验证边界见[AC追踪表](ip-optimization-ac-trace-2026-11-15.md)。新增 `node scripts/test-management-completeness.mjs` 检查详情、队列、分页及返回状态，`node docs/completeness-2026-11-15/check-go.mjs` 重放原三个契约反例。虚构容量检查需显式设置 `CFST_STORAGE_CAPACITY_TEST=1`；不发网络测速。

2026-11-15 的 8 项已知缺陷已完成修复与定向复验，结果与未实测范围见[修复复验报告](ip-optimization-fix-verification-2026-11-15.md)；这不替代部署环境的真实第三方联调和完整验收。

本地断网容器验收可运行 `sh scripts/test-management-docker.sh`；它使用独立临时命名卷并在退出时删除，不接触生产卷。

- `go test ./internal/optimizer ./internal/integrations ./internal/management ./cmd/cfst-web`
- `npm ci && npm test && npm run build`（工作目录 internal/management/web）
- `npm run test:browser && npm run test:browser:fixes && node tests/live.mjs`（工作目录 internal/management/web；需 Playwright 浏览器或通过 PLAYWRIGHT_EXECUTABLE_PATH 指定独立测试浏览器可执行文件）
- `node docs/acceptance-2026-11-15/reproduce-go.mjs && node scripts/test-management-fixes.mjs`（仓库根目录；先构建生产前端并准备 Go 依赖缓存）
- `docker compose build`
- 构建完成后，`docker compose up -d` 并检查 `docker compose ps` 与 `curl http://127.0.0.1:8080/healthz`。

以上命令是可复现步骤，不代表每种宿主环境均已验收。自动化集成测试使用注入的模拟引擎和模拟服务商；不会调用真实 DNS 写入、真实 Telegram／企业微信发送，也不会使用默认公网测速地址。
