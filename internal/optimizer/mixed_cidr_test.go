package optimizer

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func TestRegressionMixedCIDRFamilyBudgets(test *testing.T) {
	for _, cidr := range []string{"::ffff:192.0.2.0/95", "::fffe:0:0/95", "::fffc:0:0/94"} {
		for _, enableIPv4 := range []bool{false, true} {
			test.Run(fmt.Sprintf("%s/IPv4=%v", cidr, enableIPv4), func(test *testing.T) {
				config := testConfig(Source{Kind: "cidr", Value: cidr, Count: 16}, Source{Kind: "ip", Value: "198.51.100.10"})
				config.ProbeMode = "tcp"
				config.IPv4Enabled, config.IPv6Enabled = enableIPv4, true
				config.IPv4Limit, config.IPv6Limit = 1, 20
				engine := regressionEngine()
				calls := make(map[string]int)
				engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
					calls[request.IP]++
					return time.Millisecond, nil
				}
				result, err := engine.Run(context.Background(), config, nil)
				if err != nil || result.Status != "completed" {
					test.Fatalf("混合 IPv6 网段仍须正常运行: 状态=%s，错误=%v", result.Status, err)
				}
				actual := make(map[string]int)
				for _, candidate := range result.Candidates {
					actual[candidate.Family]++
				}
				test.Logf("实际候选=%v，实际探测=%d，预算=%+v", actual, len(calls), result.Budgets)
				wantIPv4 := 0
				if enableIPv4 {
					wantIPv4 = 1
				}
				if actual["ipv4"] != wantIPv4 || actual["ipv6"] != config.IPv6Limit {
					test.Fatalf("混合 IPv6 采样不得产生 IPv4 或突破 IPv4 上限: 实际=%v，IPv4预期=%d", actual, wantIPv4)
				}
				prefix := netip.MustParsePrefix(cidr).Masked()
				if !reflect.DeepEqual(result.Sources[0].CIDRs, []string{prefix.String()}) {
					test.Fatalf("混合网段必须保留 IPv6 范围: %+v", result.Sources[0])
				}
				for _, candidate := range result.Candidates {
					address := netip.MustParseAddr(candidate.IP)
					if address.Is4In6() || candidate.Family == "ipv6" && (!address.Is6() || !prefix.Contains(address)) || candidate.Family == "ipv4" && candidate.IP != "198.51.100.10" {
						test.Fatalf("候选地址必须与来源和计费地址族一致: %+v", candidate)
					}
					if calls[candidate.IP] != 1 || candidate.ProbeAttempts != 1 {
						test.Fatalf("所有候选必须按自身地址族恰好探测一次: %+v，次数=%d", candidate, calls[candidate.IP])
					}
				}
				for _, budget := range result.Budgets {
					if budget.Used != actual[budget.Family] || budget.Used != budget.Directed+budget.Sampled+budget.Expanded || budget.Used > budget.Limit || budget.Remaining != budget.Limit-budget.Used {
						test.Fatalf("预算必须与实际生成和探测一致: %+v，实际=%v", budget, actual)
					}
				}
			})
		}
	}
}

func TestRegressionMixedCIDRSamplingExcludesMappedRange(test *testing.T) {
	for _, item := range []struct{ input, canonical string }{
		{"::ffff:192.0.2.0/95", "::fffe:0:0/95"},
		{"::fffe:0:0/95", "::fffe:0:0/95"},
		{"::fffc:0:0/94", "::fffc:0:0/94"},
		{"::/79", "::/79"},
		{"::ffff:192.0.2.0/120", "192.0.2.0/24"},
		{"::ffff:0.0.0.0/96", "0.0.0.0/0"},
	} {
		test.Run(item.input, func(test *testing.T) {
			prefix := netip.MustParsePrefix(item.input)
			canonical := netip.MustParsePrefix(item.canonical)
			existing := make(map[string]*Candidate)
			for batch := 0; batch < 2; batch++ {
				addresses := samplePrefix(prefix, existing, 128)
				if len(addresses) != 128 {
					test.Fatalf("排除映射区域后仍有足够空间，不能丢弃采样名额: 批次=%d，数量=%d", batch, len(addresses))
				}
				for _, address := range addresses {
					if address.Is4In6() || !canonical.Contains(address) || address.Is4() != canonical.Addr().Is4() {
						test.Fatalf("采样结果必须属于规范前缀及同一地址族: %s，前缀=%s", address, canonical)
					}
					if existing[address.String()] != nil {
						test.Fatalf("剔除映射区间后不得重复采样: %s", address)
					}
					existing[address.String()] = &Candidate{IP: address.String()}
				}
			}
		})
	}
}

func TestRegressionSampledCandidateRequiresCanonicalAddress(test *testing.T) {
	pool := &candidatePool{byIP: make(map[string]*Candidate), sources: make(map[string]*sourceInfo)}
	mapped := netip.MustParseAddr("::ffff:192.0.2.10")
	engineCandidateAdd(pool, mapped, "source-001", "sampled")
	if len(pool.byIP) != 0 {
		test.Fatalf("采样入池不得未经前缀核验就将映射地址转成 IPv4: %+v", copyCandidates(pool))
	}
	canonical := normalizePrefix(netip.MustParsePrefix("::ffff:192.0.2.10/128")).Addr()
	engineCandidateAdd(pool, canonical, "source-002", "sampled")
	engineCandidateAdd(pool, mapped, "source-001", "sampled")
	candidate := pool.byIP[canonical.String()]
	if len(pool.byIP) != 1 || candidate == nil || candidate.Family != "ipv4" || !reflect.DeepEqual(candidate.Sources, []string{"source-002"}) {
		test.Fatalf("合法纯映射前缀先规范化后可入池，原始映射地址不得绕过核验合并: %+v", copyCandidates(pool))
	}
}
