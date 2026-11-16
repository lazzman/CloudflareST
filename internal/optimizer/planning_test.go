package optimizer

import (
	"net/netip"
	"testing"
)

func TestPrefixCapacityAcrossIntegerWidths(t *testing.T) {
	for _, prefix := range []string{
		"0.0.0.0/0", "0.0.0.0/1", "192.0.2.0/24",
		"::/0", "2001:db8::/64", "2001:db8::/65", "2001:db8::/66",
		"::fffc:0:0/94", "::fffe:0:0/95", "2001:db8::/96", "2001:db8::/97", "2001:db8::/98",
	} {
		t.Run(prefix, func(t *testing.T) {
			if got := prefixCapacity(netip.MustParsePrefix(prefix), 128); got != 128 {
				t.Fatalf("大网段容量应被请求上限截断，不能因整数宽度溢出：得到 %d，期望 128", got)
			}
		})
	}
	for _, test := range []struct {
		prefix string
		limit  int
		want   int
	}{
		{"192.0.2.0/30", 128, 4},
		{"192.0.2.0/31", 128, 2},
		{"192.0.2.1/32", 128, 1},
		{"2001:db8::/126", 128, 4},
		{"2001:db8::/127", 128, 2},
		{"2001:db8::1/128", 128, 1},
		{"192.0.2.0/30", 3, 3},
		{"2001:db8::/64", 0, 0},
		{"::/0", int(^uint(0) >> 1), int(^uint(0) >> 1)},
	} {
		t.Run(test.prefix, func(t *testing.T) {
			if got := prefixCapacity(netip.MustParsePrefix(test.prefix), test.limit); got != test.want {
				t.Fatalf("容量必须同时受网段大小和请求上限约束：得到 %d，期望 %d", got, test.want)
			}
		})
	}
}
