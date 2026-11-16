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

//go:build !windows

package task

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"syscall"

	"github.com/masgzy/CloudflareST/utils"
)

// darwin 原始 IPv4 套接字会带上 IP 头；非特权 datagram 路径与 x/net 一样请求内核剥掉。
const sysIP_STRIPHDR = 0x17

func newFamilyPinger(v6 bool) (icmpPinger, error) {
	b, err := resolveICMPBind(v6)
	if err != nil {
		return nil, err
	}
	return newSocketICMPPinger(v6, b, listenPrivileged, listenDatagramICMP)
}

func newSocketICMPPinger(v6 bool, b icmpBind, raw, datagram func(bool, icmpBind) (net.PacketConn, error)) (icmpPinger, error) {
	pc, rawErr := raw(v6, b)
	if rawErr == nil {
		if utils.Debug {
			utils.Debugf("IPv%d ICMP 使用原始套接字。", ipVersion(v6))
		}
		return newICMPEngine(pc, false, v6), nil
	}
	pc, dgramErr := datagram(v6, b)
	if dgramErr == nil {
		if utils.Debug {
			utils.Debugf("IPv%d ICMP 使用非特权 datagram（原始套接字不可用: %v）。", ipVersion(v6), rawErr)
		}
		return newICMPEngine(pc, true, v6), nil
	}
	return nil, icmpUnavailableError(rawErr, dgramErr)
}

func listenDatagramICMP(v6 bool, b icmpBind) (net.PacketConn, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("%s 不支持非特权 ICMP 套接字", runtime.GOOS)
	}
	if b.ip != nil && (b.ip.To16() == nil || (b.ip.To4() == nil) != v6) {
		return nil, fmt.Errorf("绑定地址 %s 与 IPv%d 不匹配", b.ip, ipVersion(v6))
	}
	family := syscall.AF_INET
	proto := syscall.IPPROTO_ICMP
	network := "udp4"
	if v6 {
		family = syscall.AF_INET6
		proto = syscall.IPPROTO_ICMPV6
		network = "udp6"
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM, proto)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	if runtime.GOOS == "darwin" && !v6 {
		// 剥头失败不影响收包：解析时还会再判断一次 IP 头。
		_ = syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, sysIP_STRIPHDR, 1)
	}
	if b.device != "" {
		iface, ierr := net.InterfaceByName(b.device)
		if ierr != nil {
			syscall.Close(fd)
			return nil, ierr
		}
		if berr := bindInterface(uintptr(fd), b.device, iface.Index, network); berr != nil {
			syscall.Close(fd)
			return nil, berr
		}
	}
	var sa syscall.Sockaddr
	if !v6 {
		sa4 := &syscall.SockaddrInet4{}
		if b.ip != nil {
			copy(sa4.Addr[:], b.ip.To4())
		}
		sa = sa4
	} else {
		sa6 := &syscall.SockaddrInet6{}
		if b.ip != nil {
			copy(sa6.Addr[:], b.ip.To16())
		}
		sa = sa6
	}
	if err = syscall.Bind(fd, sa); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "icmp")
	pc, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	return pc, nil
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
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, icmpRecvBuf)
	})
}
