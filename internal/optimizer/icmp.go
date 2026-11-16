package optimizer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var ErrICMPUnavailable = errors.New("本机 ICMP socket 不可用；不会回退到 TCP")

var icmpSequence uint32

func CheckICMP() (bool, string) {
	ipv4Available, ipv4Message := CheckICMPFamily("ipv4")
	_, ipv6Message := CheckICMPFamily("ipv6")
	return ipv4Available, ipv4Message + "；" + ipv6Message
}

func CheckICMPFamily(family string) (bool, string) {
	connection, _, _, _, err := openICMP(family)
	if err != nil {
		return false, err.Error()
	}
	if closeErr := connection.Close(); closeErr != nil {
		return false, fmt.Sprintf("%s ICMP socket 检查结束时关闭失败: %s", family, closeErr)
	}
	return true, fmt.Sprintf("%s ICMP socket 可用", family)
}

func openICMP(family string) (net.PacketConn, int, icmp.Type, icmp.Type, error) {
	var network, address string
	var protocol int
	var requestType, replyType icmp.Type
	switch family {
	case "ipv4":
		network, address, protocol = "udp4", "0.0.0.0", 1
		requestType, replyType = ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	case "ipv6":
		network, address, protocol = "udp6", "::", 58
		requestType, replyType = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
	default:
		return nil, 0, nil, nil, fmt.Errorf("未知地址族 %q；%w", family, ErrICMPUnavailable)
	}
	connection, err := icmp.ListenPacket(network, address)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("%s ICMP socket 检查失败: %v；%w", family, err, ErrICMPUnavailable)
	}
	return connection, protocol, requestType, replyType, nil
}

func icmpFamilyAvailable(family string) bool {
	available, _ := CheckICMPFamily(family)
	return available
}

func probeCandidate(ctx context.Context, request ProbeRequest) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if request.Mode == "tcp" {
		started := time.Now()
		connection, err := (&net.Dialer{Timeout: request.Timeout}).DialContext(ctx, "tcp", net.JoinHostPort(request.IP, fmt.Sprint(request.Port)))
		if err != nil {
			return 0, err
		}
		connection.Close()
		return time.Since(started), nil
	}
	return probeICMP(ctx, request, openICMP)
}

func probeICMP(ctx context.Context, request ProbeRequest, listen func(string) (net.PacketConn, int, icmp.Type, icmp.Type, error)) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	address, err := netip.ParseAddr(request.IP)
	if err != nil {
		return 0, fmt.Errorf("探测 IP 无效: %w", err)
	}
	family := "ipv6"
	if address.Is4() {
		family = "ipv4"
	}
	connection, protocol, requestType, replyType, err := listen(family)
	if err != nil {
		return 0, err
	}
	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	defer connection.Close()
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			connection.Close()
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		<-watcherDone
	}()
	deadline := time.Now().Add(request.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		if unavailableICMPError(err) {
			return 0, fmt.Errorf("设置 ICMP 等待时间失败: %v；%w", err, ErrICMPUnavailable)
		}
		return 0, fmt.Errorf("设置 ICMP 等待时间失败: %w", err)
	}
	value := atomic.AddUint32(&icmpSequence, 1)
	sequence := uint16(value)
	identifier := os.Getpid() & 0xffff
	if identifier == 0 {
		identifier = 1
	}
	payload := []byte(fmt.Sprintf("optimizer-%08x", value))
	message := &icmp.Message{Type: requestType, Code: 0, Body: &icmp.Echo{ID: identifier, Seq: int(sequence), Data: payload}}
	wire, err := message.Marshal(nil)
	if err != nil {
		return 0, fmt.Errorf("编码 ICMP 请求失败: %w", err)
	}
	destination := &net.UDPAddr{IP: net.IP(address.AsSlice())}
	started := time.Now()
	if _, err := connection.WriteTo(wire, destination); err != nil {
		if unavailableICMPError(err) {
			return 0, fmt.Errorf("发送 ICMP 请求失败: %v；%w", err, ErrICMPUnavailable)
		}
		return 0, err
	}
	buffer := make([]byte, 2048)
	for {
		length, source, readErr := connection.ReadFrom(buffer)
		if readErr != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if unavailableICMPError(readErr) {
				return 0, fmt.Errorf("读取 ICMP 回复失败: %v；%w", readErr, ErrICMPUnavailable)
			}
			return 0, fmt.Errorf("读取 ICMP 回复失败: %w", readErr)
		}
		response, parseErr := icmp.ParseMessage(protocol, buffer[:length])
		if parseErr != nil || response.Type != replyType {
			continue
		}
		echo, ok := response.Body.(*icmp.Echo)
		if !ok || echo.Seq != int(sequence) || string(echo.Data) != string(payload) {
			continue
		}
		sourceText := source.String()
		if peer, ok := source.(*net.UDPAddr); ok {
			sourceText = peer.IP.String()
		}
		sourceAddress, parseErr := netip.ParseAddr(sourceText)
		if parseErr != nil || sourceAddress.Unmap() != address.Unmap() {
			continue
		}
		return time.Since(started), nil
	}
}

func unavailableICMPError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return errors.Is(err, os.ErrPermission) || strings.Contains(text, "operation not permitted") || strings.Contains(text, "permission denied")
}
