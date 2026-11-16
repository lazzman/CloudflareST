# ICMP 跨平台测试

## 测试范围

- 公共单元测试不需要 root、管理员权限或公网：覆盖报文校验、目标地址匹配、超时、关停、协议族初始化失败清理、IPv6 zone 和句柄池并发回收。
- Linux / macOS 回环测试分别创建原始套接字与非特权 datagram，覆盖 IPv4 / IPv6、自动路由、源地址绑定和接口绑定。
- macOS 用套接字选项读回值验证原始 ICMP 所用的接口绑定选项，无需 root；实际 raw/datagram 收发另由回环测试覆盖。
- Windows 直接测试系统 ICMP API 的 IPv4 / IPv6 回环，并读回 IPv4 / IPv6 接口选项，检查网络字节序与主机字节序的区别。

## 本地运行

```sh
go test ./... -count=1 -timeout 180s

# Linux / macOS 竞态检测
go test -race ./... -count=1 -timeout 180s

# 跳过 ICMP 回环实测，仍运行单元测试与套接字选项检查
CFST_SKIP_ICMP_LIVE=1 go test ./task -count=1

# Linux：强制要求指定后端可用，避免缺少权限时跳过
CFST_REQUIRE_ICMP_BACKEND=raw go test ./task -run TestICMPSocketLiveLoopback -v -count=1
CFST_REQUIRE_ICMP_BACKEND=datagram go test ./task -run TestICMPSocketLiveLoopback -v -count=1
```

PowerShell 可跳过真实 ICMP 回环测试并运行其余 Windows 单测：

```powershell
$env:CFST_SKIP_ICMP_LIVE = '1'
go test ./task -count=1
```

未指定强制后端时，原始套接字权限不足会显示为 SKIP；套接字成功打开后没有收到回环应答则必须失败。Windows IPv6 回环探测套接字不可用时会跳过 IPv6 API 用例；若探测成功但 ICMP API 失败，测试必须报错。

Linux 可以在隔离容器中分别保留、移除 NET_RAW 能力来验证原始套接字与非特权降级，不需要改变宿主机权限或系统配置。

## 持续集成

`.github/workflows/test.yml` 除基础 Linux 检查和可失败不阻塞的 Go 1.26 goroutine 泄漏实验兼容性测试外，还包含以下平台检查：

1. Linux、macOS、Windows 原生执行完整单测与回环测试；Linux / macOS 运行竞态检测，Windows 运行常规测试。
2. Linux / Windows 执行 386 测试，覆盖 32 位原子操作和数值边界。
3. Go 1.23 与当前稳定版编译全部发行架构及测试程序，包括 ARMv5/6/7、MIPS 大小端、Windows ARM64 和两种 macOS 架构。
4. Linux 专项容器移除 `NET_RAW` 并开放 ping socket 组范围，强制验证无 raw 权限时的非特权 datagram 路径；基础平台 job 无权限时则允许 raw 用例显示为 SKIP。

交叉编译只验证能否构建，不等同于在目标操作系统运行测试；原生 CI 和回环测试负责补齐运行期验证。Windows API 测试只有在 Windows 原生 job 上运行。

## 跨平台 CI 边界

- 32 位任务运行完整单元测试，不只交叉编译。IPv4 / IPv6 网段容量必须按当前平台的 `int` 宽度限幅，防止较大网段容量移位后变成零或负数。
- 随机密码契约在所有平台检查文件存在、单实例互斥、退出清理和重启轮换；POSIX 的 `0700` / `0600` 权限位只在非 Windows 平台断言。Windows 使用 ACL，此测试不代表已经验证 ACL 隔离。
- 下载重定向测试使用主动刷出的正文及真实正文窗口，不假定极小响应在 Windows 上必然产生非零时长；生产代码仍拒绝零时长、零正文或真实读取失败的样本。
- `goroutineleakprofile` 是版本相关的 Go 实验开关，不保证跟随 `stable` 一直存在。可选任务固定在 Go `1.26.x` 并在该开关下运行测试，不发起公网测速；其通过只代表实验模式兼容，不等于生成了泄漏分析报告或证明没有 goroutine 泄漏。
