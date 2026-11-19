# GitHub Docker 镜像发布

## 范围与构建配置

`.github/workflows/docker-image.yml` 使用仓库根目录的 `Dockerfile` 构建管理端镜像。它与现有管理端验收、命令行测试及 Release 工作流独立运行，不修改它们，也不等待它们通过后再发布。

| 配置 | 采用值 |
| --- | --- |
| 发布分支 | `main` |
| 镜像 | `ghcr.io/lazzman/cloudflarest` |
| Dockerfile / 构建上下文 | `Dockerfile` / 仓库根目录 `.` |
| 平台 | `linux/amd64`、`linux/arm64` |
| 缓存 | GitHub Actions 缓存，作用域 `cfst-web` |
| 手动运行默认值 | `publish=false`，只构建、不推送 |

镜像地址由 `ghcr.io/${{ github.repository }}` 生成，metadata-action 会将仓库名称转为小写。在其他 fork 启用时，发布到该 fork 自己的命名空间。两种架构均配置为构建目标，不等于已经完成构建或运行验证。

管理端依赖要求 Go 1.23 或以上，Docker 构建使用 Go 1.26。现有 `Release` 工作流中的 Go 1.20 Old 任务不能直接使用这份新的 `go.mod`，需另行适配旧版依赖；本工作流不改写其发布策略。

## 触发与标签规则

`latest` 是跟随 `main` 的滚动版本，不表示稳定发行版。显式设置 `flavor: latest=false` 关闭 metadata-action 的自动 `latest`，再仅为 `main` 添加这个标签，避免版本发布覆盖它。每次发布还生成 `sha-<完整提交 SHA>`。

| 事件 / ref | 行为 | 版本或分支标签（另有 `sha-*`） |
| --- | --- | --- |
| 推送 `main` | 构建并发布 | `main`、`latest` |
| 推送 `v1.2.3` | 构建并发布 | `v1.2.3`，不更新 `latest` |
| 推送 `v1.3.0-rc.1` | 构建并发布 | `v1.3.0-rc.1`，不更新 `latest` |
| 推送 `v-experiment` | 构建并发布 | `v-experiment`，不更新 `latest` |
| 其他分支、非 `v*` 标签或 PR | 不触发此工作流 | 无 |
| 手动运行，`publish=false` | 仅构建，无 GHCR 登录或推送 | 无 |
| 手动运行 `main`，`publish=true` | 构建并发布 | `main`、`latest` |
| 手动运行 `v*` 标签，`publish=true` | 构建并发布 | 对应标签，不更新 `latest` |
| 手动运行其他 ref，`publish=true` | 明确报错，不构建、不发布 | 无 |

`v*` 只是标签名称匹配，不做 SemVer 或稳定版本校验；`v-experiment` 也会发布为独立标签。版本标签保留 `v` 前缀，非法 Docker 标签字符由 metadata-action 转换。选择特殊标签名称时需防止转换后的名称冲突。

同一镜像的分支和版本发布共用并发组，不取消正在执行的发布。该设置不保证排队顺序、每个待运行任务都会执行或 `latest` 单调变新；重跑旧的 `main` 发布仍可能把 `latest` 指回旧提交。版本和 `sha-*` 标签也可能因重跑而覆盖，需要不可变引用时使用镜像 digest。

## 启用与手动运行

1. 将工作流及它依赖的管理端代码一起提交到仓库，包括 `Dockerfile`、`.dockerignore`、`cmd/cfst-web`、`internal` 和 Go / npm 依赖文件。只上传工作流而遗漏当前本地新增文件会导致构建失败。
2. 确保仓库允许 Actions 运行及使用 `GITHUB_TOKEN` 写入关联的 GHCR 包。工作流使用自动提供的令牌，不需要新增 PAT 或把令牌写进文件。已有包可能还需允许此仓库的 Actions 访问；不要通过扩大令牌权限或关闭审批来绕过限制。
3. 配置进入 `main` 后，符合触发条件的推送会尝试构建并发布。首次发布的包默认可能为私有；匿名拉取前需由包管理员确认并设置可见性，公开仓库不等于包自动公开。
4. 在 GitHub Actions 中选择“Docker 镜像发布”，使用“Run workflow”手动运行。`publish` 默认关闭，此时只验证构建并写缓存，不上传镜像、也不导出可下载镜像。主动开启 `publish` 后才走发布路径，且仍受 ref 白名单和仓库审批约束。

现有 `Release` 工作流使用 `GITHUB_TOKEN` 创建发行版；由这个令牌产生的 tag / release 事件通常不会再次触发其他工作流。不要将“已创建 Release”等同于“已发布 Docker 镜像”。发布版本镜像需正常推送对应的 `v*` 标签，或显式以目标版本标签为 ref 手动触发本工作流并开启 `publish`；该 ref 对应提交也必须包含此工作流和完整构建文件。

## 权限与来源

顶层只有 `contents: read`；只有 `build-and-push` 任务增加 `packages: write` 并登录 GHCR。只构建任务没有包写权限，拒绝任务的权限为空。工作流不监听 PR 或 `pull_request_target`，发布任务也独立校验事件与 ref；检出代码不持久化 Git 凭据。

外部 Action 固定到完整提交 SHA，来源核对日期为 2026-11-16：

| Action | 固定提交 |
| --- | --- |
| `actions/checkout` | [11d5960a326750d5838078e36cf38b85af677262](https://github.com/actions/checkout/commit/11d5960a326750d5838078e36cf38b85af677262) |
| `docker/setup-qemu-action` | [c7c53464625b32c7a7e944ae62b3e17d2b600130](https://github.com/docker/setup-qemu-action/commit/c7c53464625b32c7a7e944ae62b3e17d2b600130) |
| `docker/setup-buildx-action` | [8d2750c68a42422c14e847fe6c8ac0403b4cbd6f](https://github.com/docker/setup-buildx-action/commit/8d2750c68a42422c14e847fe6c8ac0403b4cbd6f) |
| `docker/login-action` | [c94ce9fb468520275223c153574b00df6fe4bcc9](https://github.com/docker/login-action/commit/c94ce9fb468520275223c153574b00df6fe4bcc9) |
| `docker/metadata-action` | [c299e40c65443455700f0fdfc63efafe5b349051](https://github.com/docker/metadata-action/commit/c299e40c65443455700f0fdfc63efafe5b349051) |
| `docker/build-push-action` | [10e90e3645eae34f1e60eeb005ba3a3d33f178e8](https://github.com/docker/build-push-action/commit/10e90e3645eae34f1e60eeb005ba3a3d33f178e8) |

## 拉取与部署

首次成功发布且获得包访问权限后，可拉取相应标签，例如：

```sh
docker pull ghcr.io/lazzman/cloudflarest:latest
docker pull ghcr.io/lazzman/cloudflarest:v1.2.3
```

`v1.2.3` 是标签格式示例，不代表该镜像已存在。当前 `compose.yaml` 仍使用本地 `build: .`，不会自动切换为 GHCR 镜像。若采用预构建镜像，应将该服务的 `build` 替换为 `image: ghcr.io/lazzman/cloudflarest:<标签>`，保留端口、数据卷、环境变量、只读文件系统和权限限制；使用 digest 时完整引用为 `ghcr.io/lazzman/cloudflarest@sha256:<digest>`。

### 启动与数据保护

本地构建使用 `docker compose up -d --build`；改用预构建镜像后使用 `docker compose pull && docker compose up -d --no-build`。默认通过 `http://127.0.0.1:8080` 访问，不直接向公网开放管理端口。镜像的 `HEALTHCHECK` 会从 `CFST_LISTEN` 读取端口；修改监听端口时无需再覆盖 `healthcheck`，但 `ports` 的容器侧端口仍要与之一致。

- 未配置 `CFST_ADMIN_PASSWORD` 时，每次启动生成随机密码，可在 Docker 主机执行 `docker compose exec -u 10001 cfst-web cat /tmp/cfst/admin-password` 读取；重启后须重新读取。自定义密码由容器管理平台安全注入，不提交到仓库。
- 命名卷 `cfst-data` 保存数据库及 `master.key`，备份必须保留两者。升级前停止服务并完整备份；回退应使用与旧版本匹配的备份，不用旧程序打开新数据库，也不要多个实例共享数据卷。
- 绑定挂载会保留宿主机目录的属主和权限，但容器用户仍需实际读写权限；详见[绑定挂载权限](ip-optimization-management-deployment.md#绑定挂载权限)。
- 远程访问使用受信任的 HTTPS 反向代理并设置 `CFST_SECURE_COOKIE=true`。只有确认代理会清理来访转发头后，才通过 `CFST_TRUSTED_PROXIES` 配置可信代理的容器 CIDR。
- `NET_RAW` 用于 ICMP 探测。若部署环境不支持，应在界面切换 TCPING，不额外扩大容器权限。

构建后的断网、非 root、只读根文件系统及重启冒烟可运行 `sh scripts/test-management-docker.sh <本地镜像名>`；脚本使用独立临时容器和数据卷，退出时清理，不接触生产卷。

## 验证边界

静态检查应覆盖 YAML、事件与 ref 门控、`latest` 归属、手动构建的权限隔离、两条构建路径的一致性及 Action 来源。有 `actionlint` 时运行 `actionlint .github/workflows/docker-image.yml`；普通 YAML 解析及条件推演不替代它，也不等同于 GitHub Actions 实际运行。

配置生成和检查不代表构建、推送或部署成功。实际发布后需分别核对 Actions 结果、GHCR 标签和 digest，以及 manifest 中的 `linux/amd64`、`linux/arm64`；证明材料的附属 manifest 不算可运行平台。镜像构建成功后仍需按部署文档验证容器启动与管理端功能。

2026-11-16 的依赖审计返回 4 项既有告警（2 高、2 中），涉及 Vite、Vitest、`@vitest/mocker` 和 `source-map-js`。此次未自动升级锁定依赖。最终镜像不包含 Node、Vite 或 Vitest 运行环境，但成功构建不等于告警已经修复，开发与构建环境仍应安排依赖升级及复验。
