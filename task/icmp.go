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
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masgzy/CloudflareST/utils"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	icmpEchoTimeout = time.Second // 与 TCPing 的连接超时一致
	icmpPayloadSize = 32
	icmpRecvBuf     = 1 << 20
	ianaICMP        = 1
	ianaIPv6ICMP    = 58
)

// ICMPPing 为 true 时，延迟测速使用 ICMP Echo，而不是 TCPing / HTTPing。
var ICMPPing bool

// icmpPinger 是一条已打开的 ICMP 发送通道（原始套接字、非特权 datagram 或系统 API）。
type icmpPinger interface {
	ping(ip *net.IPAddr, timeout time.Duration) (time.Duration, bool)
	close()
}

var (
	icmpPinger4 icmpPinger
	icmpPinger6 icmpPinger
)

// icmpBind 是 -intf 解析后、某一协议族可用的绑定信息。
type icmpBind struct {
	ip     net.IP
	device string
}

type icmpWait struct {
	ch   chan time.Duration
	sent time.Time
	ip   net.IP
}

// icmpEngine 用一条共享套接字并发测多个 IP。
// 非特权 datagram 套接字会改写 Echo 的 ID，因此应答靠载荷里的令牌匹配，而不是 ID/Seq。
type icmpEngine struct {
	seq      uint64
	pc       net.PacketConn
	datagram bool
	v6       bool
	proto    int
	mu       sync.Mutex
	pending  map[uint64]*icmpWait
	closed   chan struct{}
	once     sync.Once
}

func newICMPEngine(pc net.PacketConn, datagram, v6 bool) *icmpEngine {
	proto := ianaICMP
	if v6 {
		proto = ianaIPv6ICMP
	}
	e := &icmpEngine{
		pc:       pc,
		datagram: datagram,
		v6:       v6,
		proto:    proto,
		pending:  make(map[uint64]*icmpWait),
		closed:   make(chan struct{}),
	}
	setICMPRecvBuf(pc)
	go e.readLoop()
	return e
}

func (e *icmpEngine) close() {
	e.once.Do(func() {
		close(e.closed)
		e.mu.Lock()
		e.pending = nil
		e.mu.Unlock()
		_ = e.pc.Close()
	})
}

func (e *icmpEngine) forget(token uint64) {
	e.mu.Lock()
	delete(e.pending, token)
	e.mu.Unlock()
}

func (e *icmpEngine) dst(ip *net.IPAddr) net.Addr {
	zone := ip.Zone
	if zone == "" && BindIntf != "" && ip.IP.IsLinkLocalUnicast() {
		if res, err := processInterfaceParam(BindIntf); err == nil && res.InterfaceName != "" {
			zone = res.InterfaceName
		}
	}
	if e.datagram {
		return &net.UDPAddr{IP: ip.IP, Zone: zone}
	}
	return &net.IPAddr{IP: ip.IP, Zone: zone}
}

func (e *icmpEngine) ping(ip *net.IPAddr, timeout time.Duration) (time.Duration, bool) {
	if ip == nil || ip.IP.To16() == nil || timeout <= 0 || (ip.IP.To4() == nil) != e.v6 {
		return 0, false
	}
	token := atomic.AddUint64(&e.seq, 1)
	pkt, err := marshalEcho(e.v6, token)
	if err != nil {
		return 0, false
	}
	w := &icmpWait{
		ch: make(chan time.Duration, 1),
		ip: append(net.IP(nil), ip.IP...),
	}
	e.mu.Lock()
	if e.pending == nil {
		e.mu.Unlock()
		return 0, false
	}
	w.sent = time.Now()
	e.pending[token] = w
	e.mu.Unlock()

	if _, err = e.pc.WriteTo(pkt, e.dst(ip)); err != nil {
		e.forget(token)
		noteICMPWriteErr(err)
		return 0, false
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case rtt := <-w.ch:
		return rtt, true
	case <-e.closed:
		return 0, false
	case <-timer.C:
		// 持锁判断应答是否已经入队。入队和移出 pending 在同一把锁里完成，
		// 避免“已匹配但还没写入 channel”被超时路径当成丢包。
		e.mu.Lock()
		owned := false
		if e.pending != nil {
			if _, ok := e.pending[token]; ok {
				delete(e.pending, token)
				owned = true
			}
		}
		e.mu.Unlock()
		if owned {
			return 0, false
		}
		select {
		case rtt := <-w.ch:
			return rtt, true
		default:
			return 0, false
		}
	}
}

func (e *icmpEngine) readLoop() {
	buf := make([]byte, 1500)
	for {
		n, peer, err := e.pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-e.closed:
			default:
				if utils.Debug {
					utils.Debugf("ICMP 接收循环退出: %v", err)
				}
			}
			e.close()
			return
		}
		token, ok := parseEchoToken(e.proto, buf[:n])
		if !ok {
			continue
		}
		e.mu.Lock()
		if e.pending == nil {
			e.mu.Unlock()
			continue
		}
		w := e.pending[token]
		if w != nil && w.ip.Equal(icmpPeerIP(peer)) {
			delete(e.pending, token)
			rtt := time.Since(w.sent)
			if rtt < 0 {
				rtt = 0
			}
			select {
			case w.ch <- rtt:
			default:
			}
		}
		e.mu.Unlock()
	}
}

func icmpPeerIP(peer net.Addr) net.IP {
	switch address := peer.(type) {
	case *net.IPAddr:
		return address.IP
	case *net.UDPAddr:
		return address.IP
	default:
		return nil
	}
}

func marshalEcho(v6 bool, token uint64) ([]byte, error) {
	data := make([]byte, icmpPayloadSize)
	binary.BigEndian.PutUint64(data[:8], token)
	copy(data[8:12], []byte("CFST"))
	id := int(token & 0xffff)
	if id == 0 {
		id = 1
	}
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if v6 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	msg := icmp.Message{
		Type: typ,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  int((token >> 16) & 0xffff),
			Data: data,
		},
	}
	// IPv4 校验和由 Marshal 填上；IPv6 校验和留给内核（psh 为 nil）。
	return msg.Marshal(nil)
}

// parseEchoToken 从 Echo Reply 载荷取出 8 字节令牌。
// 某些系统的原始套接字会在 ICMP 前面保留 IP 头，这里会先剥掉。
func parseEchoToken(proto int, b []byte) (uint64, bool) {
	b = stripICMPPayload(b)
	msg, err := icmp.ParseMessage(proto, b)
	if err != nil || msg.Code != 0 {
		return 0, false
	}
	switch msg.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
	default:
		return 0, false
	}
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok || len(echo.Data) != icmpPayloadSize || string(echo.Data[8:12]) != "CFST" {
		return 0, false
	}
	return binary.BigEndian.Uint64(echo.Data[:8]), true
}

func stripICMPPayload(b []byte) []byte {
	if len(b) < 20 {
		return b
	}
	switch b[0] >> 4 {
	case 4:
		ihl := int(b[0]&0x0f) * 4
		if ihl >= 20 && len(b) >= ihl+8 && b[9] == ianaICMP {
			return b[ihl:]
		}
	case 6:
		if len(b) >= 48 && b[6] == ianaIPv6ICMP {
			return b[40:]
		}
	}
	return b
}

func resolveICMPBind(v6 bool) (icmpBind, error) {
	if BindIntf == "" {
		return icmpBind{}, nil
	}
	res, err := processInterfaceParam(BindIntf)
	if err != nil {
		return icmpBind{}, err
	}
	if res.InterfaceName != "" {
		return icmpBind{device: res.InterfaceName}, nil
	}
	if res.InterfaceIPs == nil {
		return icmpBind{}, fmt.Errorf("无法解析绑定参数 %s", BindIntf)
	}
	ip := res.InterfaceIPs.IPv4
	family := "IPv4"
	if v6 {
		ip = res.InterfaceIPs.IPv6
		family = "IPv6"
	}
	if ip == nil {
		return icmpBind{}, fmt.Errorf("绑定地址 %s 不是 %s，与本次 ICMP 测速协议族不一致", BindIntf, family)
	}
	return icmpBind{ip: ip}, nil
}

func listenPrivileged(v6 bool, b icmpBind) (net.PacketConn, error) {
	network := "ip4:icmp"
	addr := "0.0.0.0"
	if v6 {
		network = "ip6:ipv6-icmp"
		addr = "::"
	}
	if b.ip != nil {
		addr = b.ip.String()
	}
	lc := net.ListenConfig{}
	if b.device != "" {
		lc.Control = getBindInterfaceControl(b.device)
	}
	return lc.ListenPacket(context.Background(), network, addr)
}

func ipVersion(v6 bool) int {
	if v6 {
		return 6
	}
	return 4
}

var icmpWriteOnce sync.Once

func noteICMPWriteErr(err error) {
	if !utils.Debug || err == nil {
		return
	}
	icmpWriteOnce.Do(func() {
		utils.Debugf("ICMP 发送失败（后续相同错误不再重复）：%v", err)
	})
}

func icmpUnavailableError(rawErr, fallbackErr error) error {
	switch runtime.GOOS {
	case "linux":
		return fmt.Errorf("无法创建 ICMP 套接字（原始套接字: %v；非特权 ping: %v）。请用 root / CAP_NET_RAW 运行，或将 net.ipv4.ping_group_range 配置为包含当前进程所属组（不要无必要地开放给所有组）。也可去掉 -ping 改用 TCPing，或使用 -httping", rawErr, fallbackErr)
	case "darwin":
		return fmt.Errorf("无法创建 ICMP 套接字（原始套接字: %v；非特权 datagram: %v）。请使用 sudo 运行，或去掉 -ping 改用 TCPing / -httping", rawErr, fallbackErr)
	case "windows":
		return fmt.Errorf("无法发送 ICMP（原始套接字: %v；系统 API: %v）。请以管理员身份运行，或去掉 -ping 改用 TCPing / -httping", rawErr, fallbackErr)
	default:
		return fmt.Errorf("无法创建 ICMP 套接字（%v；%v）。请以具备原始套接字权限的账户运行，或改用 TCPing / -httping", rawErr, fallbackErr)
	}
}

func prepareICMP(ips []*net.IPAddr) error {
	return prepareICMPWithFactory(ips, newFamilyPinger)
}

func closeICMP() {
	if icmpPinger4 != nil {
		icmpPinger4.close()
		icmpPinger4 = nil
	}
	if icmpPinger6 != nil {
		icmpPinger6.close()
		icmpPinger6 = nil
	}
}

func prepareICMPWithFactory(ips []*net.IPAddr, create func(bool) (icmpPinger, error)) error {
	closeICMP()
	need4, need6 := false, false
	for _, ip := range ips {
		if ip == nil || ip.IP.To16() == nil {
			continue
		}
		if ip.IP.To4() != nil {
			need4 = true
		} else {
			need6 = true
		}
		if need4 && need6 {
			break
		}
	}
	if need4 {
		p, err := create(false)
		if err != nil {
			return err
		}
		icmpPinger4 = p
	}
	if need6 {
		p, err := create(true)
		if err != nil {
			closeICMP()
			return err
		}
		icmpPinger6 = p
	}
	return nil
}

func (p *Ping) ensureICMP() {
	if err := prepareICMP(p.ips); err != nil {
		utils.Error("ICMP 延迟测速不可用：%v", err)
		os.Exit(1)
	}
}

func icmpEcho(ip *net.IPAddr, timeout time.Duration) (time.Duration, bool) {
	if ip == nil || ip.IP == nil {
		return 0, false
	}
	var pinger icmpPinger
	if ip.IP.To4() != nil {
		pinger = icmpPinger4
	} else {
		pinger = icmpPinger6
	}
	if pinger == nil {
		return 0, false
	}
	return pinger.ping(ip, timeout)
}

func (p *Ping) icmping(ip *net.IPAddr) (recv int, totalDelay time.Duration, colo string) {
	for i := 0; i < PingTimes; i++ {
		if atomic.LoadInt32(&p.earlyStop) == 1 || atomic.LoadInt32(&GlobalEarlyStop) == 1 {
			return
		}
		if delay, ok := icmpEcho(ip, icmpEchoTimeout); ok {
			recv++
			totalDelay += delay
			if PingInterval > 0 && i < PingTimes-1 {
				time.Sleep(PingInterval)
			}
		}
	}
	return
}

func winIPv4Addr(ip net.IP) (uint32, bool) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, false
	}
	// Windows IPAddr 把网络序的 4 字节放进 ULONG。小端机器上按内存原样解释才是 1.2.3.4。
	return binary.LittleEndian.Uint32(ip4), true
}

func parseWinICMPv4Reply(reply []byte) (time.Duration, bool) {
	if len(reply) < 12 {
		return 0, false
	}
	if binary.LittleEndian.Uint32(reply[4:8]) != 0 {
		return 0, false
	}
	return time.Duration(binary.LittleEndian.Uint32(reply[8:12])) * time.Millisecond, true
}

const (
	winICMPv6StatusOff = 28
	winICMPv6RTTOff    = 32
	afINET6            = 23
)

func parseWinICMPv6Reply(reply []byte) (time.Duration, bool) {
	if len(reply) < winICMPv6RTTOff+4 {
		return 0, false
	}
	if binary.LittleEndian.Uint32(reply[winICMPv6StatusOff:winICMPv6StatusOff+4]) != 0 {
		return 0, false
	}
	return time.Duration(binary.LittleEndian.Uint32(reply[winICMPv6RTTOff:winICMPv6RTTOff+4])) * time.Millisecond, true
}

func sockaddrV6(ip net.IP, scope uint32) []byte {
	b := make([]byte, 28)
	binary.LittleEndian.PutUint16(b[0:2], afINET6)
	if ip != nil {
		if ip16 := ip.To16(); ip16 != nil && ip.To4() == nil {
			copy(b[8:24], ip16)
		}
	}
	binary.LittleEndian.PutUint32(b[24:28], scope)
	return b
}

func zoneToScope(zone string) (uint32, bool) {
	if zone == "" {
		return 0, true
	}
	if n, err := strconv.ParseUint(zone, 10, 32); err == nil {
		return uint32(n), true
	} else if numberError, ok := err.(*strconv.NumError); ok && numberError.Err == strconv.ErrRange {
		return 0, false
	}
	iface, err := net.InterfaceByName(zone)
	if err != nil || iface.Index <= 0 {
		return 0, false
	}
	return uint32(iface.Index), true
}

type scopedIP struct {
	ip    net.IP
	scope uint32
	rank  int
}

type hostAddr struct {
	index int
	ip    net.IP
}

func pickBestSource(addrs []hostAddr, v6 bool) (scopedIP, error) {
	var best *scopedIP
	for _, addr := range addrs {
		ip := addr.ip
		if ip == nil {
			continue
		}
		rank := 0
		scope := uint32(0)
		if v6 {
			if ip.To4() != nil {
				continue
			}
			switch {
			case ip.IsLoopback():
				rank = 1
			case ip.IsLinkLocalUnicast():
				rank = 2
				scope = uint32(addr.index)
			case ip.IsGlobalUnicast():
				rank = 3
			default:
				continue
			}
		} else {
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			ip = ip4
			switch {
			case ip.IsLoopback():
				rank = 1
			case ip.IsLinkLocalUnicast():
				rank = 2
			case ip.IsGlobalUnicast():
				rank = 3
			default:
				continue
			}
		}
		if best != nil && rank <= best.rank {
			continue
		}
		address := ip.To16()
		if !v6 {
			address = ip.To4()
		}
		cp := append(net.IP(nil), address...)
		best = &scopedIP{ip: cp, scope: scope, rank: rank}
	}
	if best == nil {
		family := "IPv4"
		if v6 {
			family = "IPv6"
		}
		return scopedIP{}, fmt.Errorf("没有可用的 %s 源地址", family)
	}
	return *best, nil
}
