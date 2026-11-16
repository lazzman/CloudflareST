package optimizer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type permissionPacketConn struct {
	stage  string
	closed atomic.Int32
}

func (connection *permissionPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, &net.OpError{Op: "read", Net: "udp4", Err: os.ErrPermission}
}

func (connection *permissionPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	if connection.stage == "write" {
		return 0, &net.OpError{Op: "write", Net: "udp4", Err: errors.New("operation not permitted")}
	}
	return len(payload), nil
}

func (connection *permissionPacketConn) Close() error {
	connection.closed.Add(1)
	return nil
}

func (connection *permissionPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4zero}
}

func (connection *permissionPacketConn) SetDeadline(time.Time) error {
	if connection.stage == "deadline" {
		return fmt.Errorf("受控权限失败: %w", os.ErrPermission)
	}
	return nil
}

func (connection *permissionPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (connection *permissionPacketConn) SetWriteDeadline(time.Time) error { return nil }

func TestICMPPermissionFailuresAreFatalAndCloseSocket(t *testing.T) {
	for _, stage := range []string{"deadline", "write", "read"} {
		t.Run(stage, func(t *testing.T) {
			connection := &permissionPacketConn{stage: stage}
			listen := func(family string) (net.PacketConn, int, icmp.Type, icmp.Type, error) {
				if family != "ipv4" {
					t.Errorf("错误地址族: %s", family)
				}
				return connection, 1, ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply, nil
			}
			elapsed, err := probeICMP(context.Background(), ProbeRequest{IP: "192.0.2.1", Mode: "icmp", Timeout: time.Second}, listen)
			if !errors.Is(err, ErrICMPUnavailable) || elapsed != 0 || connection.closed.Load() != 1 {
				t.Fatalf("权限失败不能降为普通丢包，且须关闭套接字: %v，%s，关闭次数=%d", err, elapsed, connection.closed.Load())
			}
		})
	}
}

func TestICMPReadPermissionFailureStopsEngine(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"})
	config.IPv4Limit, config.ProbeCount = 1, 5
	engine := successfulEngine()
	calls := 0
	engine.Probe = func(ctx context.Context, request ProbeRequest) (time.Duration, error) {
		calls++
		return probeICMP(ctx, request, func(string) (net.PacketConn, int, icmp.Type, icmp.Type, error) {
			return &permissionPacketConn{stage: "read"}, 1, ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply, nil
		})
	}
	result, err := engine.Run(context.Background(), config, nil)
	if !errors.Is(err, ErrICMPUnavailable) || result.Status != "failed" || calls != 1 || result.Candidates[0].ProbeFailures != 1 {
		t.Fatalf("ICMP 权限错误必须立即失败，不得按普通丢包重复探测: %+v，%v，调用=%d", result, err, calls)
	}
}
