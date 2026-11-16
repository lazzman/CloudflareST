package task

import (
	"errors"
	"net"
	"testing"
	"time"
	"unsafe"
)

func icmpLoopbackInterface(t *testing.T) net.Interface {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			return iface
		}
	}
	t.Fatal("没有可用的回环接口")
	return net.Interface{}
}

type closingICMPPinger struct {
	scriptPinger
	closed int
}

func (pinger *closingICMPPinger) close() { pinger.closed++ }

func TestPrepareICMPCleansUpFamilies(t *testing.T) {
	old4, old6 := icmpPinger4, icmpPinger6
	icmpPinger4, icmpPinger6 = nil, nil
	t.Cleanup(func() {
		closeICMP()
		icmpPinger4, icmpPinger6 = old4, old6
	})
	previous4, previous6 := &closingICMPPinger{}, &closingICMPPinger{}
	icmpPinger4, icmpPinger6 = previous4, previous6
	current4 := &closingICMPPinger{}
	failure := errors.New("IPv6 不可用")
	factory := func(ipv6 bool) (icmpPinger, error) {
		if ipv6 {
			return nil, failure
		}
		return current4, nil
	}
	addresses := []*net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("::1")}}
	if err := prepareICMPWithFactory(addresses, factory); !errors.Is(err, failure) {
		t.Fatalf("错误没有传播: %v", err)
	}
	if previous4.closed != 1 || previous6.closed != 1 || current4.closed != 1 || icmpPinger4 != nil || icmpPinger6 != nil {
		t.Fatal("重新初始化或部分协议族失败时没有释放资源")
	}
	current4, current6 := &closingICMPPinger{}, &closingICMPPinger{}
	if err := prepareICMPWithFactory(addresses, func(ipv6 bool) (icmpPinger, error) {
		if ipv6 {
			return current6, nil
		}
		return current4, nil
	}); err != nil {
		t.Fatal(err)
	}
	closeICMP()
	closeICMP()
	if current4.closed != 1 || current6.closed != 1 {
		t.Fatal("清理不应遗漏或重复关闭协议族资源")
	}
	if err := prepareICMPWithFactory([]*net.IPAddr{nil, {}, {IP: net.IP{1}}}, func(bool) (icmpPinger, error) {
		t.Fatal("无效地址不应创建 ICMP 后端")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestICMPAtomicAlignment(t *testing.T) {
	engine := newICMPEngine(newScriptConn(0, true), true, false)
	defer engine.close()
	if uintptr(unsafe.Pointer(&engine.seq))%8 != 0 {
		t.Fatal("ICMP 序号没有按 8 字节对齐，32 位平台的原子操作会崩溃")
	}
	if _, ok := engine.ping(&net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, time.Second); !ok {
		t.Fatal("当前平台的原子递增或应答匹配失败")
	}
}

func TestZoneToScopeUint32(t *testing.T) {
	for _, test := range []struct {
		zone  string
		want  uint32
		valid bool
	}{
		{"", 0, true}, {"0", 0, true}, {"12", 12, true},
		{"4294967295", ^uint32(0), true}, {"4294967296", 0, false}, {"4294967297", 0, false},
		{"-1", 0, false}, {"18446744073709551615", 0, false},
	} {
		t.Run(test.zone, func(t *testing.T) {
			if got, valid := zoneToScope(test.zone); got != test.want || valid != test.valid {
				t.Fatalf("scope=%d valid=%v，期望 %d valid=%v", got, valid, test.want, test.valid)
			}
		})
	}
}

func TestParseEchoTokenRequiresValidPayload(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		protocol, replyType := ianaICMP, byte(0)
		if ipv6 {
			protocol, replyType = ianaIPv6ICMP, 129
		}
		packet, err := marshalEcho(ipv6, 42)
		if err != nil {
			t.Fatal(err)
		}
		packet[0] = replyType
		packet[1] = 1
		if _, ok := parseEchoToken(protocol, packet); ok {
			t.Fatalf("IPv6=%v，错误的 Echo Code 被当成成功应答", ipv6)
		}
		packet[1] = 0
		packet[16] ^= 0xff
		if _, ok := parseEchoToken(protocol, packet); ok {
			t.Fatalf("IPv6=%v，不属于本程序的载荷被当成成功应答", ipv6)
		}
	}
}

func TestICMPEngineRejectsReplyFromOtherIP(t *testing.T) {
	for _, datagram := range []bool{false, true} {
		conn := newScriptConn(0, true)
		conn.peer = &net.IPAddr{IP: net.ParseIP("127.0.0.2")}
		engine := newICMPEngine(conn, datagram, false)
		_, ok := engine.ping(&net.IPAddr{IP: net.ParseIP("127.0.0.1")}, 20*time.Millisecond)
		engine.close()
		if ok {
			t.Fatal("其他 IP 的应答被错误计入当前目标")
		}
	}
}

type observedICMPConn struct {
	*scriptConn
	written chan struct{}
	readEnd chan struct{}
}

func (conn *observedICMPConn) WriteTo(packet []byte, address net.Addr) (int, error) {
	count, err := conn.scriptConn.WriteTo(packet, address)
	close(conn.written)
	return count, err
}

func (conn *observedICMPConn) ReadFrom(packet []byte) (int, net.Addr, error) {
	count, peer, err := conn.scriptConn.ReadFrom(packet)
	if err != nil {
		close(conn.readEnd)
	}
	return count, peer, err
}

func TestICMPEngineShutdownUnblocksPending(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		name := "主动关闭"
		if readFailure {
			name = "接收失败"
		}
		t.Run(name, func(t *testing.T) {
			conn := &observedICMPConn{
				scriptConn: newScriptConn(10*time.Second, false),
				written:    make(chan struct{}),
				readEnd:    make(chan struct{}),
			}
			engine := newICMPEngine(conn, true, false)
			t.Cleanup(engine.close)
			result := make(chan bool, 1)
			go func() {
				_, ok := engine.ping(&net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, 5*time.Second)
				result <- ok
			}()
			select {
			case <-conn.written:
			case <-time.After(time.Second):
				t.Fatal("发送没有开始")
			}
			if readFailure {
				_ = conn.scriptConn.Close()
			} else {
				engine.close()
			}
			select {
			case ok := <-result:
				if ok {
					t.Fatal("关闭时仍返回成功")
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("关闭后仍在等待单次 ICMP 超时")
			}
			select {
			case <-conn.readEnd:
			case <-time.After(time.Second):
				t.Fatal("接收循环没有退出")
			}
			if _, ok := engine.ping(&net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, time.Second); ok {
				t.Fatal("关闭后不应重新发送")
			}
		})
	}
}
