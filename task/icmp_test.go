// CloudflareST - Cloudflare CDN IP 延迟和速度测试工具
// Copyright (C) 2020-2026 masgzy <https://github.com/masgzy>
//
// 本程序使用 GPL-3.0 协议开源。
// 完整许可证文本见项目根目录 LICENSE 文件。
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package task

import (
	"encoding/binary"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseEchoTokenRoundTrip(t *testing.T) {
	token := uint64(0x1122334455667788)
	for _, v6 := range []bool{false, true} {
		pkt, err := marshalEcho(v6, token)
		if err != nil {
			t.Fatalf("v6=%v marshal: %v", v6, err)
		}
		// 改写 ID，模拟非特权套接字或 NAT 只保留载荷的情况。
		if len(pkt) < 6 {
			t.Fatalf("v6=%v 报文过短: %d", v6, len(pkt))
		}
		pkt[4] ^= 0xff
		proto := ianaICMP
		pkt[0] = 0 // Echo Reply
		if v6 {
			proto = ianaIPv6ICMP
			pkt[0] = 129
		}
		got, ok := parseEchoToken(proto, pkt)
		if !ok || got != token {
			t.Fatalf("v6=%v token=%x ok=%v", v6, got, ok)
		}
	}
}

func TestParseEchoTokenStripsIPv4Header(t *testing.T) {
	token := uint64(0xaabbccddeeff0011)
	pkt, err := marshalEcho(false, token)
	if err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 24) // IHL=6
	hdr[0] = 0x46
	hdr[9] = ianaICMP
	pkt[0] = 0 // Echo Reply
	got, ok := parseEchoToken(ianaICMP, append(hdr, pkt...))
	if !ok || got != token {
		t.Fatalf("token=%x ok=%v", got, ok)
	}
}

func TestParseEchoTokenStripsIPv6Header(t *testing.T) {
	token := uint64(42)
	pkt, err := marshalEcho(true, token)
	if err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 40)
	hdr[0] = 0x60
	hdr[6] = ianaIPv6ICMP
	pkt[0] = 129 // Echo Reply
	got, ok := parseEchoToken(ianaIPv6ICMP, append(hdr, pkt...))
	if !ok || got != token {
		t.Fatalf("token=%x ok=%v", got, ok)
	}
}

func TestParseEchoTokenIgnoreNonEcho(t *testing.T) {
	if _, ok := parseEchoToken(ianaICMP, []byte{3, 0, 0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("目的不可达不应当成 Echo Reply")
	}
	if _, ok := parseEchoToken(ianaICMP, []byte{0, 0}); ok {
		t.Fatal("过短报文不应匹配")
	}
}

func TestICMPLiveLoopback(t *testing.T) {
	if os.Getenv("CFST_SKIP_ICMP_LIVE") == "1" {
		t.Skip("已跳过实网 ICMP")
	}
	p, err := newFamilyPinger(false)
	if err != nil {
		t.Skipf("当前环境无法发送 ICMP: %v", err)
	}
	defer p.close()

	ip := &net.IPAddr{IP: net.ParseIP("127.0.0.1")}
	delay, ok := p.ping(ip, time.Second)
	if !ok {
		t.Fatal("回环 ICMP 未收到应答")
	}
	if delay < 0 || delay > time.Second {
		t.Fatalf("回环 RTT 异常: %s", delay)
	}
	t.Logf("127.0.0.1 RTT %s", delay)

	const n = 32
	var wg sync.WaitGroup
	fail := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := p.ping(ip, time.Second); !ok {
				fail <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(fail)
	if _, failed := <-fail; failed {
		t.Fatal("并发回环 ICMP 存在丢失，令牌匹配可能串包或漏收")
	}
}

func TestParseEchoTokenRejectsRequestAndBadHeader(t *testing.T) {
	token := uint64(0x0102030405060708)
	pkt, err := marshalEcho(false, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseEchoToken(ianaICMP, pkt); ok {
		t.Fatal("Echo 请求不应计为应答")
	}
	pkt[0] = 0
	hdr := make([]byte, 20)
	hdr[0] = 0x45
	hdr[9] = 6 // TCP，不能当成 ICMP 头剥掉
	if _, ok := parseEchoToken(ianaICMP, append(hdr, pkt...)); ok {
		t.Fatal("非 ICMP 的 IPv4 头被错误剥离")
	}
	short := append([]byte{0x60, 0, 0, 0, 0, 0, 17}, make([]byte, 20)...)
	if _, ok := parseEchoToken(ianaIPv6ICMP, append(short, pkt...)); ok {
		t.Fatal("过短或下一首部不是 IPv6-ICMP 的报文被错误剥离")
	}
	if _, ok := parseEchoToken(ianaICMP, nil); ok {
		t.Fatal("空报文不应匹配")
	}
}

func TestParseEchoTokenEndian(t *testing.T) {
	token := uint64(0x1122334455667788)
	buf := make([]byte, 8+icmpPayloadSize)
	binary.BigEndian.PutUint64(buf[8:], token)
	copy(buf[16:20], "CFST")
	got, ok := parseEchoToken(ianaICMP, buf)
	if !ok || got != token {
		t.Fatalf("token=%x ok=%v", got, ok)
	}
}

func TestWinICMPReplyParsing(t *testing.T) {
	addr, ok := winIPv4Addr(net.ParseIP("1.2.3.4"))
	if !ok || addr != 0x04030201 {
		t.Fatalf("IPv4 地址编码 = %#x ok=%v", addr, ok)
	}
	if _, ok = winIPv4Addr(net.ParseIP("2001:db8::1")); ok {
		t.Fatal("IPv6 不应编码成 IPv4 地址")
	}
	reply := make([]byte, 12)
	binary.LittleEndian.PutUint32(reply[8:12], 15)
	d, ok := parseWinICMPv4Reply(reply)
	if !ok || d != 15*time.Millisecond {
		t.Fatalf("v4 reply %s ok=%v", d, ok)
	}
	binary.LittleEndian.PutUint32(reply[4:8], 11010)
	if _, ok = parseWinICMPv4Reply(reply); ok {
		t.Fatal("非成功状态不应视为应答")
	}
	if _, ok = parseWinICMPv4Reply([]byte{1, 2, 3}); ok {
		t.Fatal("过短应答不应成功")
	}

	v6 := make([]byte, winICMPv6RTTOff+4)
	binary.LittleEndian.PutUint32(v6[winICMPv6RTTOff:winICMPv6RTTOff+4], 8)
	d, ok = parseWinICMPv6Reply(v6)
	if !ok || d != 8*time.Millisecond {
		t.Fatalf("v6 reply %s ok=%v", d, ok)
	}
	binary.LittleEndian.PutUint32(v6[winICMPv6StatusOff:winICMPv6StatusOff+4], 1)
	if _, ok = parseWinICMPv6Reply(v6); ok {
		t.Fatal("IPv6 失败状态不应视为应答")
	}
}

func TestSockaddrV6AndZone(t *testing.T) {
	b := sockaddrV6(nil, 9)
	if got := binary.LittleEndian.Uint16(b[0:2]); got != afINET6 {
		t.Fatalf("family=%d", got)
	}
	if binary.LittleEndian.Uint32(b[24:28]) != 9 {
		t.Fatalf("scope=%d", binary.LittleEndian.Uint32(b[24:28]))
	}
	ip := net.ParseIP("2001:db8::1")
	b = sockaddrV6(ip, 0)
	if !net.IP(b[8:24]).Equal(ip) {
		t.Fatalf("地址未写入: %x", b[8:24])
	}
	b = sockaddrV6(net.ParseIP("1.2.3.4"), 0)
	if net.IP(b[8:24]).IsGlobalUnicast() {
		t.Fatal("IPv4 不应写入 IPv6 套接字地址")
	}
	if scope, valid := zoneToScope("no-such-if-cfst"); scope != 0 || valid {
		t.Fatal("无效 zone 未被拒绝")
	}
	if scope, valid := zoneToScope("15"); scope != 15 || !valid {
		t.Fatal("数字 zone 未按接口索引解析")
	}
}

func TestPickBestSource(t *testing.T) {
	v4 := []hostAddr{
		{index: 1, ip: net.ParseIP("127.0.0.1")},
		{index: 2, ip: net.ParseIP("169.254.1.1")},
		{index: 3, ip: net.ParseIP("192.168.1.9")},
		{index: 4, ip: net.ParseIP("10.0.0.8")},
	}
	best, err := pickBestSource(v4, false)
	if err != nil || !best.ip.Equal(net.ParseIP("192.168.1.9")) {
		t.Fatalf("应优先第一个全局 IPv4，得到 %v %v", best.ip, err)
	}
	if len(best.ip) != net.IPv4len {
		t.Fatalf("Windows IPv4 源地址应使用 4 字节格式，实际长度 %d", len(best.ip))
	}
	v6 := []hostAddr{
		{index: 1, ip: net.ParseIP("::1")},
		{index: 4, ip: net.ParseIP("fe80::1")},
		{index: 5, ip: net.ParseIP("2001:db8::9")},
	}
	best, err = pickBestSource(v6, true)
	if err != nil || !best.ip.Equal(net.ParseIP("2001:db8::9")) || best.scope != 0 {
		t.Fatalf("应选全局 IPv6，得到 %v scope=%d err=%v", best.ip, best.scope, err)
	}
	best, err = pickBestSource(v6[:2], true)
	if err != nil || !best.ip.Equal(net.ParseIP("fe80::1")) || best.scope != 4 {
		t.Fatalf("只有链路本地时应带接口索引，得到 %v scope=%d err=%v", best.ip, best.scope, err)
	}
	if _, err = pickBestSource(nil, true); err == nil {
		t.Fatal("没有地址时应失败")
	}
}

func TestResolveICMPBind(t *testing.T) {
	orig := BindIntf
	t.Cleanup(func() { BindIntf = orig })

	BindIntf = ""
	b, err := resolveICMPBind(false)
	if err != nil || b.ip != nil || b.device != "" {
		t.Fatalf("空绑定 = %+v %v", b, err)
	}
	BindIntf = "127.0.0.1:9"
	b, err = resolveICMPBind(false)
	if err != nil || !b.ip.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("IPv4 绑定 = %v %v", b.ip, err)
	}
	if _, err = resolveICMPBind(true); err == nil {
		t.Fatal("IPv4 绑定不应用于 IPv6")
	}
	BindIntf = "::1"
	b, err = resolveICMPBind(true)
	if err != nil || !b.ip.Equal(net.ParseIP("::1")) {
		t.Fatalf("IPv6 绑定 = %v %v", b.ip, err)
	}
	if _, err = resolveICMPBind(false); err == nil {
		t.Fatal("IPv6 绑定不应用于 IPv4")
	}
	BindIntf = "no-such-if-cfst"
	if _, err = resolveICMPBind(false); err == nil {
		t.Fatal("不存在的接口应失败")
	}
	iface := firstInterface(t)
	BindIntf = iface.Name
	b, err = resolveICMPBind(false)
	if err != nil || b.device != iface.Name {
		t.Fatalf("接口名绑定 = %+v %v", b, err)
	}
}

func TestICMPUnavailableErrorIncludesCause(t *testing.T) {
	err := icmpUnavailableError(os.ErrPermission, os.ErrClosed)
	msg := err.Error()
	if !strings.Contains(msg, os.ErrPermission.Error()) || !strings.Contains(msg, os.ErrClosed.Error()) {
		t.Fatalf("错误信息未保留原因: %s", msg)
	}
}

func TestICMPEchoDispatchAndEarlyStop(t *testing.T) {
	oldTimes, oldInterval := PingTimes, PingInterval
	old4, old6 := icmpPinger4, icmpPinger6
	oldStop := atomic.LoadInt32(&GlobalEarlyStop)
	t.Cleanup(func() {
		PingTimes, PingInterval = oldTimes, oldInterval
		icmpPinger4, icmpPinger6 = old4, old6
		atomic.StoreInt32(&GlobalEarlyStop, oldStop)
	})
	PingInterval = 0
	PingTimes = 4
	fake := &scriptPinger{results: []bool{true, false, true, false}, delay: 10 * time.Millisecond}
	icmpPinger4 = fake
	ip := &net.IPAddr{IP: net.ParseIP("1.2.3.4")}
	recv, total, colo := (&Ping{}).icmping(ip)
	if recv != 2 || total != 20*time.Millisecond || colo != "" || fake.calls != 4 {
		t.Fatalf("recv=%d total=%s colo=%q calls=%d", recv, total, colo, fake.calls)
	}

	atomic.StoreInt32(&GlobalEarlyStop, 1)
	fake.calls = 0
	recv, _, _ = (&Ping{}).icmping(ip)
	if recv != 0 || fake.calls != 0 {
		t.Fatalf("全局停止后仍继续测速 recv=%d calls=%d", recv, fake.calls)
	}
	atomic.StoreInt32(&GlobalEarlyStop, 0)

	fake = &scriptPinger{results: []bool{true, true, true, true}, delay: time.Millisecond}
	fake.onCall = func(n int) {
		if n == 0 {
			atomic.StoreInt32(&GlobalEarlyStop, 1)
		}
	}
	icmpPinger4 = fake
	recv, _, _ = (&Ping{}).icmping(ip)
	if recv != 1 || fake.calls != 1 {
		t.Fatalf("第一次成功后应停止，recv=%d calls=%d", recv, fake.calls)
	}
	atomic.StoreInt32(&GlobalEarlyStop, 0)

	if _, ok := icmpEcho(nil, time.Second); ok {
		t.Fatal("空地址不应成功")
	}
	icmpPinger4 = nil
	if _, ok := icmpEcho(ip, time.Second); ok {
		t.Fatal("没有 IPv4 通道时不应成功")
	}
	icmpPinger6 = &scriptPinger{results: []bool{true}, delay: 5 * time.Millisecond}
	v6 := &net.IPAddr{IP: net.ParseIP("2001:db8::1")}
	d, ok := icmpEcho(v6, time.Second)
	if !ok || d != 5*time.Millisecond {
		t.Fatalf("IPv6 未走到对应通道: %s %v", d, ok)
	}
}

func TestICMPIntervalOnlyAfterSuccess(t *testing.T) {
	oldTimes, oldInterval := PingTimes, PingInterval
	old4 := icmpPinger4
	t.Cleanup(func() {
		PingTimes, PingInterval = oldTimes, oldInterval
		icmpPinger4 = old4
	})
	PingTimes = 2
	PingInterval = 40 * time.Millisecond
	icmpPinger4 = &scriptPinger{results: []bool{true, true}, delay: time.Millisecond}
	start := time.Now()
	recv, _, _ := (&Ping{}).icmping(&net.IPAddr{IP: net.ParseIP("1.2.3.4")})
	elapsed := time.Since(start)
	if recv != 2 || elapsed < 30*time.Millisecond {
		t.Fatalf("成功之间应等待间隔 recv=%d elapsed=%s", recv, elapsed)
	}

	PingInterval = 150 * time.Millisecond
	icmpPinger4 = &scriptPinger{results: []bool{false, true}}
	start = time.Now()
	recv, _, _ = (&Ping{}).icmping(&net.IPAddr{IP: net.ParseIP("1.2.3.4")})
	elapsed = time.Since(start)
	if recv != 1 || elapsed > 80*time.Millisecond {
		t.Fatalf("失败后不应等待间隔 recv=%d elapsed=%s", recv, elapsed)
	}
}

func TestICMPEngineMatchesPayloadNotID(t *testing.T) {
	conn := newScriptConn(0, true)
	e := newICMPEngine(conn, true, false)
	defer e.close()
	ip := &net.IPAddr{IP: net.ParseIP("10.0.0.1")}
	delay, ok := e.ping(ip, time.Second)
	if !ok || delay < 0 || delay > time.Second {
		t.Fatalf("改写 ID 后应仍能匹配载荷 ok=%v rtt=%s", ok, delay)
	}
	if _, ok = e.ping(nil, time.Second); ok {
		t.Fatal("空地址不应发送")
	}
}

func TestICMPEngineTimeoutAndStaleReply(t *testing.T) {
	conn := newScriptConn(80*time.Millisecond, false)
	e := newICMPEngine(conn, true, false)
	defer e.close()
	ip := &net.IPAddr{IP: net.ParseIP("10.0.0.2")}
	start := time.Now()
	if _, ok := e.ping(ip, 25*time.Millisecond); ok || time.Since(start) > 70*time.Millisecond {
		t.Fatal("超时时应尽快失败，不能把迟到应答算成功")
	}
	time.Sleep(90 * time.Millisecond)
	conn.setDelay(0)
	delay, ok := e.ping(ip, time.Second)
	if !ok || delay > 200*time.Millisecond {
		t.Fatalf("迟到的旧应答不应配给下一次 ping ok=%v rtt=%s", ok, delay)
	}
}

func TestICMPEngineConcurrentDelays(t *testing.T) {
	conn := newScriptConn(0, true)
	conn.delays = map[string]time.Duration{
		"10.1.0.1": 20 * time.Millisecond,
		"10.1.0.2": 120 * time.Millisecond,
	}
	e := newICMPEngine(conn, false, false)
	defer e.close()
	fast := &net.IPAddr{IP: net.ParseIP("10.1.0.1")}
	slow := &net.IPAddr{IP: net.ParseIP("10.1.0.2")}
	var fastRTT, slowRTT time.Duration
	var fastOK, slowOK bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		fastRTT, fastOK = e.ping(fast, time.Second)
	}()
	go func() {
		defer wg.Done()
		slowRTT, slowOK = e.ping(slow, time.Second)
	}()
	wg.Wait()
	if !fastOK || !slowOK {
		t.Fatalf("并发测速失败 fast=%v slow=%v", fastOK, slowOK)
	}
	if slowRTT <= fastRTT {
		t.Fatalf("不同目标的 RTT 被串包: fast=%s slow=%s", fastRTT, slowRTT)
	}
}

func TestICMPEngineClose(t *testing.T) {
	conn := newScriptConn(time.Second, false)
	e := newICMPEngine(conn, true, false)
	e.close()
	start := time.Now()
	_, ok := e.ping(&net.IPAddr{IP: net.ParseIP("10.0.0.3")}, time.Second)
	if ok || time.Since(start) > 200*time.Millisecond {
		t.Fatal("关闭后 ping 应立即失败")
	}
	e.close()
}

func TestICMPEngineIPv6Reply(t *testing.T) {
	conn := newScriptConn(0, true)
	conn.replyType = 129
	e := newICMPEngine(conn, true, true)
	defer e.close()
	delay, ok := e.ping(&net.IPAddr{IP: net.ParseIP("2001:db8::2")}, time.Second)
	if !ok || delay > time.Second {
		t.Fatalf("IPv6 应答未匹配 ok=%v rtt=%s", ok, delay)
	}
}

func TestLinkLocalZoneFromInterface(t *testing.T) {
	iface := firstInterface(t)
	orig := BindIntf
	t.Cleanup(func() { BindIntf = orig })
	BindIntf = iface.Name
	e := &icmpEngine{datagram: true}
	addr, ok := e.dst(&net.IPAddr{IP: net.ParseIP("fe80::1")}).(*net.UDPAddr)
	if !ok || addr.Zone != iface.Name {
		t.Fatalf("链路本地应带上绑定接口 zone=%v", e.dst(&net.IPAddr{IP: net.ParseIP("fe80::1")}))
	}
}

type scriptPinger struct {
	results []bool
	delay   time.Duration
	calls   int
	onCall  func(int)
}

func (s *scriptPinger) ping(ip *net.IPAddr, timeout time.Duration) (time.Duration, bool) {
	n := s.calls
	s.calls++
	if s.onCall != nil {
		s.onCall(n)
	}
	if n < len(s.results) && !s.results[n] {
		return 0, false
	}
	return s.delay, true
}

func (s *scriptPinger) close() {}

type scriptReply struct {
	packet []byte
	peer   net.Addr
}

type scriptConn struct {
	mu        sync.Mutex
	replies   chan scriptReply
	closed    chan struct{}
	once      sync.Once
	delay     time.Duration
	delays    map[string]time.Duration
	rewriteID bool
	replyType byte
	peer      net.Addr
}

func newScriptConn(delay time.Duration, rewriteID bool) *scriptConn {
	return &scriptConn{
		replies:   make(chan scriptReply, 32),
		closed:    make(chan struct{}),
		delay:     delay,
		rewriteID: rewriteID,
	}
}

func (c *scriptConn) setDelay(d time.Duration) {
	c.mu.Lock()
	c.delay = d
	c.mu.Unlock()
}

func (c *scriptConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	reply := append([]byte(nil), b...)
	if len(reply) > 0 {
		if c.replyType != 0 {
			reply[0] = c.replyType
		} else {
			reply[0] = 0
		}
	}
	if c.rewriteID && len(reply) >= 6 {
		reply[4] = 0xab
		reply[5] = 0xcd
	}
	delay := c.delayFor(addr)
	peer := addr
	if c.peer != nil {
		peer = c.peer
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-c.closed:
		case <-timer.C:
			select {
			case <-c.closed:
			case c.replies <- scriptReply{packet: reply, peer: peer}:
			}
		}
	}()
	return len(b), nil
}

func (c *scriptConn) delayFor(addr net.Addr) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.delays != nil && addr != nil {
		host := addr.String()
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if d, ok := c.delays[host]; ok {
			return d
		}
	}
	return c.delay
}

func (c *scriptConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case pkt := <-c.replies:
		n := copy(b, pkt.packet)
		return n, pkt.peer, nil
	}
}

func (c *scriptConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *scriptConn) LocalAddr() net.Addr { return &net.IPAddr{} }

func (c *scriptConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptConn) SetWriteDeadline(time.Time) error { return nil }

func firstInterface(t *testing.T) *net.Interface {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Name == "" {
			continue
		}
		return &ifaces[i]
	}
	t.Skip("没有可用的网络接口")
	return nil
}
