package task

import (
	"syscall"
	"testing"
)

func TestDarwinICMPInterfaceSocketOptions(t *testing.T) {
	iface := icmpLoopbackInterface(t)
	for _, test := range []struct {
		network string
		family  int
		level   int
		option  int
	}{
		{"ip4", syscall.AF_INET, syscall.IPPROTO_IP, IP_BOUND_IF},
		{"ip6", syscall.AF_INET6, syscall.IPPROTO_IPV6, IPV6_BOUND_IF},
	} {
		t.Run(test.network, func(t *testing.T) {
			fd, err := syscall.Socket(test.family, syscall.SOCK_DGRAM, syscall.IPPROTO_UDP)
			if err != nil {
				t.Fatal(err)
			}
			defer syscall.Close(fd)
			if err := bindDarwin(fd, iface.Index, test.network); err != nil {
				t.Fatal(err)
			}
			index, err := syscall.GetsockoptInt(fd, test.level, test.option)
			if err != nil || index != iface.Index {
				t.Fatalf("接口绑定未生效: index=%d want=%d err=%v", index, iface.Index, err)
			}
		})
	}
}
