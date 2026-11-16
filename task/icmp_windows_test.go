//go:build windows

package task

import (
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWindowsSystemICMPv4Loopback(t *testing.T) {
	if os.Getenv("CFST_SKIP_ICMP_LIVE") == "1" {
		t.Skip("已跳过实网 ICMP")
	}
	pinger, err := newSystemICMP(false, icmpBind{})
	if err != nil {
		t.Fatalf("创建 Windows IPv4 ICMP API 后端失败: %v", err)
	}
	defer pinger.close()

	delay, ok := pinger.ping(&net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, time.Second)
	if !ok || delay < 0 || delay > time.Second {
		t.Fatalf("Windows IPv4 ICMP API 回环失败: ok=%v RTT=%s", ok, delay)
	}
	pinger.close()
	if _, ok := pinger.ping(&net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, time.Second); ok {
		t.Fatal("关闭后的 Windows ICMP API 后端仍能发送")
	}
}

func TestWindowsSourceRespectsAddressFamily(t *testing.T) {
	if source, scope, err := windowsSource(false, icmpBind{}); err != nil || source != nil || scope != 0 {
		t.Fatalf("未绑定时 IPv4 应交给系统选择源地址: source=%v scope=%d err=%v", source, scope, err)
	}
	if source, scope, err := windowsSource(true, icmpBind{}); err != nil || source != nil || scope != 0 {
		t.Fatalf("未绑定时 IPv6 应交给系统选择源地址: source=%v scope=%d err=%v", source, scope, err)
	}
	if _, _, err := windowsSource(false, icmpBind{ip: net.ParseIP("::1")}); err == nil {
		t.Fatal("IPv6 源地址不能用于 IPv4")
	}
	if _, _, err := windowsSource(true, icmpBind{ip: net.IPv4(127, 0, 0, 1)}); err == nil {
		t.Fatal("IPv4 源地址不能用于 IPv6")
	}
}

func TestWindowsIPv6SystemICMPLoopback(t *testing.T) {
	if os.Getenv("CFST_SKIP_ICMP_LIVE") == "1" {
		t.Skip("已跳过实网 ICMP")
	}
	probe, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		t.Skipf("此 Windows 环境未启用 IPv6 回环: %v", err)
	}
	_ = probe.Close()
	pinger, err := newSystemICMP(true, icmpBind{})
	if err != nil {
		t.Fatalf("创建 Windows IPv6 ICMP API 后端失败: %v", err)
	}
	defer pinger.close()
	delay, ok := pinger.ping(&net.IPAddr{IP: net.ParseIP("::1")}, time.Second)
	if !ok {
		t.Fatal("Windows IPv6 ICMP API 回环未收到应答")
	}
	if delay < 0 || delay > time.Second {
		t.Fatalf("Windows IPv6 ICMP API RTT 异常: %s", delay)
	}
}

func TestWindowsICMPInterfaceSocketOptions(t *testing.T) {
	iface := icmpLoopbackInterface(t)
	for _, test := range []struct {
		network string
		udp     string
		address string
		level   int
		option  int
		index   int
	}{
		{"ip4", "udp4", "127.0.0.1:0", syscall.IPPROTO_IP, IP_UNICAST_IF, iface.Index},
		{"ip6", "udp6", "[::1]:0", syscall.IPPROTO_IPV6, IPV6_UNICAST_IF, iface.Index},
	} {
		t.Run(test.network, func(t *testing.T) {
			conn, err := net.ListenPacket(test.udp, test.address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			raw, err := conn.(*net.UDPConn).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			index := 0
			var optionErr error
			controlErr := raw.Control(func(fd uintptr) {
				optionErr = bindInterface(fd, iface.Name, iface.Index, test.network)
				if optionErr == nil {
					index, optionErr = syscall.GetsockoptInt(syscall.Handle(fd), test.level, test.option)
				}
			})
			if controlErr != nil || optionErr != nil || index != test.index {
				t.Fatalf("接口索引或字节序错误: index=%d want=%d option=%v control=%v", index, test.index, optionErr, controlErr)
			}
		})
	}
}
