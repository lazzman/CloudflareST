package optimizer

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func regressionEngine() *Engine {
	return &Engine{
		Concurrency: 1,
		Probe: func(context.Context, ProbeRequest) (time.Duration, error) {
			return time.Millisecond, nil
		},
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("回归测试禁止真实 DNS 解析")
		},
		Download: func(context.Context, DownloadRequest) (DownloadSample, error) {
			return DownloadSample{}, errors.New("回归测试禁止真实下载")
		},
		ICMPCheck: func(string) error {
			return errors.New("回归测试禁止真实 ICMP 检查")
		},
	}
}

func TestRegressionMappedCIDRNormalization(test *testing.T) {
	for _, item := range []struct{ input, canonical string }{
		{"::ffff:192.0.2.129/120", "192.0.2.0/24"},
		{"::ffff:c000:220/124", "192.0.2.32/28"},
		{"::ffff:192.0.2.1/128", "192.0.2.1/32"},
		{"::ffff:192.0.2.1/96", "0.0.0.0/0"},
		{"192.0.2.129/24", "192.0.2.0/24"},
		{"2001:db8::1234/120", "2001:db8::1200/120"},
	} {
		test.Run(item.input, func(test *testing.T) {
			config := testConfig(Source{Kind: "cidr", Value: item.input, Count: 1})
			config.IPv6Enabled = true
			if err := Validate(config); err != nil {
				test.Fatalf("合法映射 CIDR 不应被拒绝: %v", err)
			}
			pool, err := regressionEngine().collectSources(context.Background(), config)
			if err != nil {
				test.Fatal(err)
			}
			info := pool.sources["source-001"]
			want := netip.MustParsePrefix(item.canonical)
			if !reflect.DeepEqual(info.prefixes, []netip.Prefix{want}) || !reflect.DeepEqual(info.result.CIDRs, []string{item.canonical}) {
				test.Fatalf("来源及规划必须使用等价前缀 %s: %+v", item.canonical, info)
			}
			if info.result.Source != config.Sources[0] {
				test.Fatal("规范化不得改写原始来源快照")
			}
		})
	}
}

func TestRegressionMappedCIDRFamilyBudget(test *testing.T) {
	for _, limit := range []int{1, 5, 10} {
		test.Run(fmt.Sprint(limit), func(test *testing.T) {
			config := testConfig(Source{Kind: "ip", Value: "198.51.100.10"}, Source{Kind: "cidr", Value: "::ffff:192.0.2.0/120", Count: 4})
			config.ProbeMode = "tcp"
			config.IPv6Enabled = true
			config.IPv4Limit, config.IPv6Limit = limit, 10
			engine := regressionEngine()
			calls := make(map[string]int)
			engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
				calls[request.IP]++
				return time.Millisecond, nil
			}
			result, err := engine.Run(context.Background(), config, nil)
			if err != nil || result.Status != "completed" || len(result.Candidates) != limit {
				test.Fatalf("映射来源必须遵循 IPv4 累计上限: %+v，%v", result, err)
			}
			if len(result.Budgets) != 2 || result.Budgets[0].Used != limit || result.Budgets[0].Remaining != 0 || result.Budgets[1].Used != 0 || result.Budgets[1].RequestedInitial != 0 {
				test.Fatalf("实际候选与分族预算必须一致: %+v", result.Budgets)
			}
			for _, candidate := range result.Candidates {
				if candidate.Family != "ipv4" || !netip.MustParseAddr(candidate.IP).Is4() || calls[candidate.IP] != 1 {
					test.Fatalf("映射候选必须以 IPv4 去重探测: %+v，%v", candidate, calls)
				}
			}
		})
	}
}

func TestRegressionMappedCIDRDisabledIPv4(test *testing.T) {
	for _, withIPv6 := range []bool{false, true} {
		test.Run(fmt.Sprint(withIPv6), func(test *testing.T) {
			config := testConfig(Source{Kind: "cidr", Value: "::ffff:192.0.2.0/120", Count: 4})
			config.ProbeMode = "tcp"
			config.IPv4Enabled, config.IPv6Enabled, config.IPv6Limit = false, true, 10
			wantCount := 0
			if withIPv6 {
				config.Sources = append(config.Sources, Source{Kind: "cidr", Value: "2001:db8::1/128", Count: 1})
				wantCount = 1
			}
			engine := regressionEngine()
			engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
				if !netip.MustParseAddr(request.IP).Is6() {
					test.Errorf("关闭 IPv4 后不得探测 %s", request.IP)
				}
				return time.Millisecond, nil
			}
			result, err := engine.Run(context.Background(), config, nil)
			if (err == nil) != withIPv6 || len(result.Candidates) != wantCount {
				test.Fatalf("映射来源不得绕过 IPv4 开关: %+v，%v", result, err)
			}
			if result.Sources[0].Status != "skipped" || len(result.Sources[0].CIDRs) != 0 || len(result.Budgets) != 1 || result.Budgets[0].Family != "ipv6" || result.Budgets[0].Used != wantCount {
				test.Fatalf("禁用映射来源不得占用 IPv6 预算: %+v", result)
			}
		})
	}
}

func TestRegressionMappedCIDREquivalentSamplingAndExpansion(test *testing.T) {
	var baseline Result
	for _, sources := range [][]Source{
		{{Kind: "cidr", Value: "192.0.2.0/28", Count: 1}},
		{{Kind: "cidr", Value: "::ffff:192.0.2.0/124", Count: 1}},
		{{Kind: "cidr", Value: "192.0.2.0/28", Count: 1}, {Kind: "cidr", Value: "::ffff:192.0.2.0/124", Count: 1}},
	} {
		config := testConfig(sources...)
		config.ProbeMode, config.IPv4Limit = "tcp", 300
		result, err := regressionEngine().Run(context.Background(), config, nil)
		if err != nil || len(result.Candidates) != 16 {
			test.Fatalf("映射 CIDR 与 IPv4 CIDR 必须生成相同候选: %+v，%v", result, err)
		}
		if baseline.Candidates == nil {
			baseline = result
		}
		budget := result.Budgets[0]
		if budget.RequestedInitial != 1 || budget.Sampled != 1 || budget.Expanded != 15 || budget.Used != 16 {
			test.Fatalf("等价前缀不得重复请求、重复采样或借用 IPv6 配额: %+v", budget)
		}
		for index, candidate := range result.Candidates {
			previous := baseline.Candidates[index]
			if candidate.IP != previous.IP || candidate.Origin != previous.Origin || candidate.Family != "ipv4" || candidate.ProbeAttempts != 1 || len(candidate.Sources) != len(sources) {
				test.Fatalf("等价来源的采样、扩采及来源合并不一致: %+v / %+v", candidate, previous)
			}
		}
	}
}

func TestRegressionSourceCancellationPreservesFacts(test *testing.T) {
	for _, runEngine := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			test.Run(fmt.Sprintf("运行=%v/部分解析=%v", runEngine, partial), func(test *testing.T) {
				config := testConfig(
					Source{Kind: "cidr", Value: "::ffff:192.0.2.0/124"},
					Source{Kind: "domain", Value: "first.example"},
					Source{Kind: "domain", Value: "failed.example"},
					Source{Kind: "ip", Value: "2001:db8::1"},
					Source{Kind: "domain", Value: "cancelled.example"},
					Source{Kind: "domain", Value: "unstarted.example"},
				)
				config.ProbeMode = "tcp"
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				engine := regressionEngine()
				engine.Resolve = func(ctx context.Context, domain string) ([]netip.Addr, error) {
					switch domain {
					case "first.example":
						return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
					case "failed.example":
						return nil, errors.New("受控解析失败")
					case "cancelled.example":
						cancel()
						if partial {
							return []netip.Addr{netip.MustParseAddr("::ffff:192.0.2.11")}, ctx.Err()
						}
						return nil, ctx.Err()
					default:
						test.Error("取消后不得解析未开始的来源")
						return nil, ctx.Err()
					}
				}
				engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
					test.Error("来源解析取消后不得开始探测")
					return 0, context.Canceled
				}
				completedSources := 0
				progress := func(event Progress) {
					if event.Stage == "sources" {
						completedSources = event.Done
					}
				}
				var result Result
				var err error
				wantState := "queued"
				if runEngine {
					result, err = engine.Run(ctx, config, progress)
					wantState = "cancelled"
					if result.Status != "cancelled" {
						test.Fatalf("运行必须报告取消: %+v", result)
					}
				} else {
					var pool *candidatePool
					pool, err = engine.collectSourcesWithProgress(ctx, config, progress)
					if pool == nil {
						test.Fatal("取消必须返回已积累的来源池")
					}
					result.Sources, result.Candidates = sourceResults(pool), copyCandidates(pool)
				}
				if !errors.Is(err, context.Canceled) || completedSources != 4 || len(result.Sources) != 6 {
					test.Fatalf("取消不得抹去来源事实和已完成进度: %+v，%v，进度=%d", result, err, completedSources)
				}
				for index, status := range []string{"completed", "completed", "failed", "skipped", "cancelled", "cancelled"} {
					if source := result.Sources[index]; source.Status != status || status != "completed" && source.Error == "" {
						test.Fatalf("来源状态不得回退或伪装完成: %+v，预期=%s", source, status)
					}
				}
				if !reflect.DeepEqual(result.Sources[1].ResolvedIPs, []string{"192.0.2.10"}) || !reflect.DeepEqual(result.Sources[0].CIDRs, []string{"192.0.2.0/28"}) {
					test.Fatalf("已取得的解析地址或 CIDR 丢失: %+v", result.Sources)
				}
				wantCount := 1
				if partial {
					wantCount++
					if !reflect.DeepEqual(result.Sources[4].ResolvedIPs, []string{"192.0.2.11"}) {
						test.Fatal("当前来源已取得的部分解析地址不得丢失")
					}
				}
				if len(result.Candidates) != wantCount || result.Sources[0].CandidateCount != wantCount || result.Sources[1].CandidateCount != 1 {
					test.Fatalf("取消时须保留候选及所有已解析来源关联: %+v", result)
				}
				for _, candidate := range result.Candidates {
					if candidate.State != wantState || candidate.Eligible || candidate.Rank != 0 || candidate.ProbeAttempts != 0 || candidate.LatencyMS != nil || len(candidate.Sources) != 2 {
						test.Fatalf("未测候选不得丢失或伪装合格: %+v", candidate)
					}
					if runEngine && (candidate.Reason == "" || len(candidate.Steps) == 0) {
						test.Fatalf("取消候选必须保留未完成原因: %+v", candidate)
					}
				}
			})
		}
	}
}

func TestRegressionSourceProgressCancellation(test *testing.T) {
	for _, cancelAfter := range []int{0, 1, 2} {
		test.Run(fmt.Sprint(cancelAfter), func(test *testing.T) {
			config := testConfig(Source{Kind: "domain", Value: "first.example"}, Source{Kind: "domain", Value: "second.example"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			engine := regressionEngine()
			resolved := 0
			engine.Resolve = func(context.Context, string) ([]netip.Addr, error) {
				resolved++
				return []netip.Addr{netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", resolved))}, nil
			}
			result, err := engine.Run(ctx, config, func(event Progress) {
				if event.Stage == "sources" && event.Done == cancelAfter {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) || result.Status != "cancelled" || resolved != cancelAfter || len(result.Candidates) != resolved || len(result.Sources) != 2 {
				test.Fatalf("来源进度回调取消必须停止后续解析并保留事实: %+v，%v，解析次数=%d", result, err, resolved)
			}
			for index, source := range result.Sources {
				want := "cancelled"
				if index < resolved {
					want = "completed"
				}
				if source.Status != want {
					test.Fatalf("已完成来源与未开始来源必须区分: %+v", source)
				}
			}
			for _, candidate := range result.Candidates {
				if candidate.State != "cancelled" || candidate.Eligible || candidate.ProbeAttempts != 0 {
					test.Fatalf("来源阶段取消不得开始探测: %+v", candidate)
				}
			}
		})
	}
}

func TestRegressionExpansionAllMatchingSources(test *testing.T) {
	for _, item := range []struct{ direct, cidr, equivalent, family string }{
		{"192.0.2.200", "192.0.2.0/28", "::ffff:192.0.2.0/124", "ipv4"},
		{"2001:db8::c8", "2001:db8::/124", "2001:db8::f/124", "ipv6"},
	} {
		test.Run(item.family, func(test *testing.T) {
			config := testConfig(Source{Kind: "ip", Value: item.direct}, Source{Kind: "cidr", Value: item.cidr, Count: 1}, Source{Kind: "cidr", Value: item.equivalent, Count: 1})
			config.ProbeMode = "tcp"
			config.IPv4Enabled, config.IPv6Enabled = item.family == "ipv4", item.family == "ipv6"
			config.IPv4Limit, config.IPv6Limit = 300, 300
			engine := regressionEngine()
			calls := make(map[string]int)
			engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
				calls[request.IP]++
				if request.IP == item.direct {
					return time.Millisecond, nil
				}
				return 10 * time.Millisecond, nil
			}
			result, err := engine.Run(context.Background(), config, nil)
			if err != nil || len(result.Candidates) != 256 {
				test.Fatalf("定向触发必须扩满邻近范围: %+v，%v", result, err)
			}
			matched := 0
			prefix := netip.MustParsePrefix(item.cidr)
			for _, candidate := range result.Candidates {
				if calls[candidate.IP] != 1 || candidate.ProbeAttempts != 1 {
					test.Fatalf("补齐来源不得重复探测: %+v", candidate)
				}
				if prefix.Contains(netip.MustParseAddr(candidate.IP)) {
					matched++
					if !reflect.DeepEqual(candidate.Sources, []string{"source-001", "source-002", "source-003"}) || candidate.Origin == "direct" {
						test.Fatalf("必须保留触发来源、全部命中来源及生成方式: %+v", candidate)
					}
				}
			}
			if matched != 16 || result.Sources[1].CandidateCount != 16 || result.Sources[2].CandidateCount != 16 {
				test.Fatalf("所选网段来源的候选计数必须包含扩采: %+v", result.Sources)
			}
			budget := result.Budgets[0]
			if budget.Directed != 1 || budget.Sampled != 1 || budget.Expanded != 254 || budget.Used != 256 || len(budget.Expansions) != 1 || !reflect.DeepEqual(budget.Expansions[0].Triggers, []string{item.direct}) || !reflect.DeepEqual(budget.Expansions[0].Sources, []string{"source-001"}) {
				test.Fatalf("补齐来源不得改变预算或触发记录: %+v", budget)
			}
		})
	}
}
