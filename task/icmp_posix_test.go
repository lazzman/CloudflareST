//go:build !windows

package task

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

func TestICMPSocketBackendFallback(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		for _, rawOK := range []bool{false, true} {
			t.Run(fmt.Sprintf("IPv6=%v/raw=%v", ipv6, rawOK), func(t *testing.T) {
				bind := icmpBind{device: "测试接口"}
				rawCalls, datagramCalls := 0, 0
				conn := newScriptConn(0, true)
				raw := func(version bool, got icmpBind) (net.PacketConn, error) {
					rawCalls++
					if version != ipv6 || got.device != bind.device {
						t.Fatal("原始套接字没有收到协议族和接口绑定参数")
					}
					if !rawOK {
						return nil, os.ErrPermission
					}
					return conn, nil
				}
				datagram := func(version bool, got icmpBind) (net.PacketConn, error) {
					datagramCalls++
					if version != ipv6 || got.device != bind.device {
						t.Fatal("非特权套接字没有保留协议族和接口绑定参数")
					}
					return conn, nil
				}
				pinger, err := newSocketICMPPinger(ipv6, bind, raw, datagram)
				if err != nil {
					t.Fatal(err)
				}
				defer pinger.close()
				engine := pinger.(*icmpEngine)
				if engine.datagram == rawOK || engine.v6 != ipv6 || rawCalls != 1 {
					t.Fatalf("错误的后端选择: datagram=%v IPv6=%v rawCalls=%d", engine.datagram, engine.v6, rawCalls)
				}
				if (datagramCalls == 1) == rawOK {
					t.Fatalf("非特权后端调用次数错误: %d", datagramCalls)
				}
			})
		}
	}
	fail := func(bool, icmpBind) (net.PacketConn, error) { return nil, os.ErrPermission }
	if pinger, err := newSocketICMPPinger(false, icmpBind{}, fail, fail); err == nil || pinger != nil {
		t.Fatal("两个后端均失败时不应静默退回 TCPing")
	}
}

func TestICMPDatagramRejectsWrongFamily(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		address := net.ParseIP("::1")
		if ipv6 {
			address = net.ParseIP("127.0.0.1")
		}
		conn, err := listenDatagramICMP(ipv6, icmpBind{ip: address})
		if conn != nil {
			_ = conn.Close()
		}
		if err == nil {
			t.Fatal("协议族不匹配时绑定被静默忽略")
		}
	}
}

func TestICMPSocketLiveLoopback(t *testing.T) {
	if os.Getenv("CFST_SKIP_ICMP_LIVE") == "1" {
		t.Skip("已跳过实网 ICMP")
	}
	iface := icmpLoopbackInterface(t)
	for _, backend := range []struct {
		name     string
		datagram bool
		listen   func(bool, icmpBind) (net.PacketConn, error)
	}{
		{"raw", false, listenPrivileged},
		{"datagram", true, listenDatagramICMP},
	} {
		for _, ipv6 := range []bool{false, true} {
			address := net.ParseIP("127.0.0.1")
			if ipv6 {
				address = net.ParseIP("::1")
			}
			for _, bind := range []struct {
				name string
				bind icmpBind
			}{
				{"自动路由", icmpBind{}},
				{"源地址", icmpBind{ip: address}},
				{"接口", icmpBind{device: iface.Name}},
			} {
				t.Run(fmt.Sprintf("%s/IPv%d/%s", backend.name, ipVersion(ipv6), bind.name), func(t *testing.T) {
					conn, err := backend.listen(ipv6, bind.bind)
					if err != nil {
						if os.Getenv("CFST_REQUIRE_ICMP_BACKEND") != backend.name && errors.Is(err, os.ErrPermission) {
							t.Skipf("当前账户没有 %s 权限: %v", backend.name, err)
						}
						t.Fatal(err)
					}
					engine := newICMPEngine(conn, backend.datagram, ipv6)
					defer engine.close()
					for attempt := 0; attempt < 3; attempt++ {
						delay, ok := engine.ping(&net.IPAddr{IP: address}, time.Second)
						if !ok || delay < 0 || delay > time.Second {
							t.Fatalf("回环应答异常: ok=%v RTT=%s", ok, delay)
						}
					}
				})
			}
		}
	}
}
