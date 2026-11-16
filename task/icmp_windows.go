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

//go:build windows

package task

import (
	"fmt"
	"net"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/masgzy/CloudflareST/utils"
)

var (
	iphlpapi            = syscall.NewLazyDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmp6CreateFile = iphlpapi.NewProc("Icmp6CreateFile")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
	procIcmpSendEcho2Ex = iphlpapi.NewProc("IcmpSendEcho2Ex")
	procIcmp6SendEcho2  = iphlpapi.NewProc("Icmp6SendEcho2")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
)

var windowsICMPAPITip sync.Once

func newFamilyPinger(v6 bool) (icmpPinger, error) {
	b, err := resolveICMPBind(v6)
	if err != nil {
		return nil, err
	}
	pc, rawErr := listenPrivileged(v6, b)
	if rawErr == nil {
		if utils.Debug {
			utils.Debugf("IPv%d ICMP 使用原始套接字。", ipVersion(v6))
		}
		return newICMPEngine(pc, false, v6), nil
	}
	p, apiErr := newSystemICMP(v6, b)
	if apiErr == nil {
		windowsICMPAPITip.Do(func() {
			utils.Tip("ICMP 原始套接字不可用，已改用系统 ICMP API（延迟精度为毫秒）。如需亚毫秒精度，请以管理员身份运行。")
		})
		return p, nil
	}
	return nil, icmpUnavailableError(rawErr, apiErr)
}

func setICMPRecvBuf(pc net.PacketConn) {
	sc, ok := pc.(syscall.Conn)
	if !ok {
		return
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, icmpRecvBuf)
	})
}

type winICMP struct {
	v6      bool
	src     net.IP
	scope   uint32
	device  string
	send    *syscall.LazyProc
	handles *icmpHandlePool
}

func newSystemICMP(v6 bool, b icmpBind) (icmpPinger, error) {
	src, scope, err := windowsSource(v6, b)
	if err != nil {
		return nil, err
	}
	send := procIcmpSendEcho2Ex
	if v6 {
		send = procIcmp6SendEcho2
	}
	if err := send.Find(); err != nil {
		if v6 || src != nil {
			return nil, fmt.Errorf("系统 ICMP API 不支持指定的协议或源地址: %w", err)
		}
		send = procIcmpSendEcho
		if err := send.Find(); err != nil {
			return nil, err
		}
	}
	if err := procIcmpCloseHandle.Find(); err != nil {
		return nil, err
	}
	probe, err := createICMPHandle(v6)
	if err != nil {
		return nil, err
	}
	w := &winICMP{
		v6:     v6,
		src:    src,
		scope:  scope,
		device: b.device,
		send:   send,
		handles: &icmpHandlePool{
			idle: []uintptr{uintptr(probe)},
			create: func() (uintptr, error) {
				handle, err := createICMPHandle(v6)
				return uintptr(handle), err
			},
			destroy: func(handle uintptr) {
				_, _, _ = procIcmpCloseHandle.Call(handle)
			},
		},
	}
	return w, nil
}

func (w *winICMP) close() {
	w.handles.close()
}

func (w *winICMP) ping(ip *net.IPAddr, timeout time.Duration) (time.Duration, bool) {
	if ip == nil || ip.IP.To16() == nil || timeout <= 0 || (ip.IP.To4() == nil) != w.v6 {
		return 0, false
	}
	handle := w.handles.take()
	if handle == 0 {
		return 0, false
	}
	defer w.handles.release(handle)
	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	if w.v6 {
		return w.ping6(syscall.Handle(handle), ip, ms)
	}
	return w.ping4(syscall.Handle(handle), ip.IP, ms)
}

func (w *winICMP) ping4(h syscall.Handle, dst net.IP, timeoutMS uint32) (time.Duration, bool) {
	dstAddr, ok := winIPv4Addr(dst)
	if !ok {
		return 0, false
	}
	data := []byte("CloudflareST-ICMP-echo-request!!")
	reply := make([]byte, 256+len(data))
	var srcAddr uintptr
	if addr, ok := winIPv4Addr(w.src); ok {
		srcAddr = uintptr(addr)
	}
	var r1 uintptr
	var callErr error
	if w.send == procIcmpSendEcho2Ex {
		r1, _, callErr = w.send.Call(
			uintptr(h),
			0, 0, 0,
			srcAddr,
			uintptr(dstAddr),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			0,
			uintptr(unsafe.Pointer(&reply[0])),
			uintptr(len(reply)),
			uintptr(timeoutMS),
		)
	} else {
		r1, _, callErr = w.send.Call(
			uintptr(h),
			uintptr(dstAddr),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			0,
			uintptr(unsafe.Pointer(&reply[0])),
			uintptr(len(reply)),
			uintptr(timeoutMS),
		)
	}
	runtime.KeepAlive(data)
	runtime.KeepAlive(reply)
	if r1 == 0 {
		if utils.Debug && callErr != nil {
			noteICMPWriteErr(callErr)
		}
		return 0, false
	}
	return parseWinICMPv4Reply(reply)
}

func (w *winICMP) ping6(h syscall.Handle, dst *net.IPAddr, timeoutMS uint32) (time.Duration, bool) {
	if dst == nil || dst.IP == nil {
		return 0, false
	}
	ip6 := dst.IP.To16()
	if ip6 == nil || dst.IP.To4() != nil {
		return 0, false
	}
	src := sockaddrV6(w.src, w.scope)
	scope, ok := zoneToScope(dst.Zone)
	if !ok {
		return 0, false
	}
	if scope == 0 && dst.Zone == "" && dst.IP.IsLinkLocalUnicast() {
		scope, _ = zoneToScope(w.device)
		if scope == 0 {
			scope = w.scope
		}
	}
	to := sockaddrV6(ip6, scope)
	data := []byte("CloudflareST-ICMP-echo-request!!")
	reply := make([]byte, 512+len(data))
	r1, _, callErr := w.send.Call(
		uintptr(h),
		0, 0, 0,
		uintptr(unsafe.Pointer(&src[0])),
		uintptr(unsafe.Pointer(&to[0])),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0,
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(len(reply)),
		uintptr(timeoutMS),
	)
	runtime.KeepAlive(src)
	runtime.KeepAlive(to)
	runtime.KeepAlive(data)
	runtime.KeepAlive(reply)
	if r1 == 0 {
		if utils.Debug && callErr != nil {
			noteICMPWriteErr(callErr)
		}
		return 0, false
	}
	return parseWinICMPv6Reply(reply)
}

func createICMPHandle(v6 bool) (syscall.Handle, error) {
	proc := procIcmpCreateFile
	if v6 {
		proc = procIcmp6CreateFile
	}
	if err := proc.Find(); err != nil {
		return 0, err
	}
	r1, _, e := proc.Call()
	h := syscall.Handle(r1)
	if h == 0 || h == syscall.InvalidHandle {
		if e != nil && e != syscall.Errno(0) {
			return 0, e
		}
		return 0, fmt.Errorf("创建 ICMP 句柄失败")
	}
	return h, nil
}

func windowsSource(v6 bool, b icmpBind) (net.IP, uint32, error) {
	if b.device == "" {
		if b.ip != nil && (b.ip.To16() == nil || (b.ip.To4() == nil) != v6) {
			return nil, 0, fmt.Errorf("绑定地址 %s 与 IPv%d 不匹配", b.ip, ipVersion(v6))
		}
		return b.ip, 0, nil
	}
	best, err := pickScopedIP(b.device, v6)
	if err != nil {
		return nil, 0, err
	}
	return best.ip, best.scope, nil
}

func pickScopedIP(device string, v6 bool) (scopedIP, error) {
	var ifaces []net.Interface
	if device != "" {
		iface, err := net.InterfaceByName(device)
		if err != nil {
			return scopedIP{}, err
		}
		ifaces = []net.Interface{*iface}
	} else {
		all, err := net.Interfaces()
		if err != nil {
			return scopedIP{}, err
		}
		ifaces = all
	}
	var list []hostAddr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP == nil {
				continue
			}
			list = append(list, hostAddr{index: iface.Index, ip: ipnet.IP})
		}
	}
	best, err := pickBestSource(list, v6)
	if err != nil && device != "" {
		family := "IPv4"
		if v6 {
			family = "IPv6"
		}
		return scopedIP{}, fmt.Errorf("接口 %s 上没有可用的 %s 地址", device, family)
	}
	return best, err
}
